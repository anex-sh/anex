package glami

import (
	"context"
	"path"
	"strings"

	"gitlab.devklarka.cz/ai/gpu-provider/virtualpod"
	"k8s.io/api/core/v1"
	v2 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (p *Provider) loadMountedConfigMaps(ctx context.Context, pod *v1.Pod) map[string]map[string]string {
	out := make(map[string]map[string]string)
	if p == nil || p.k8s == nil || pod == nil {
		return out
	}

	// --- 1) Gather referenced ConfigMap names (deduplicated) ---
	configMaps := make(map[string]struct{})
	add := func(name string) {
		if name == "" || name == "kube-root-ca.crt" {
			return
		}
		if _, ok := configMaps[name]; !ok {
			configMaps[name] = struct{}{}
		}
	}

	// Volumes (including projected volumes)
	for _, vol := range pod.Spec.Volumes {
		if strings.HasPrefix(vol.Name, "kube-api-access-") {
			continue
		}
		if vol.ConfigMap != nil {
			add(vol.ConfigMap.Name)
		}
		if vol.Projected != nil {
			for _, src := range vol.Projected.Sources {
				// TODO: Support other sources like Secrets and DownwardAPI
				if src.ConfigMap != nil {
					add(src.ConfigMap.Name)
				}
			}
		}
	}

	// --- 2) Fetch each ConfigMap and copy its contents into the result map ---
	for name := range configMaps {
		cm, err := p.k8s.CoreV1().ConfigMaps(pod.Namespace).Get(ctx, name, v2.GetOptions{})
		if err != nil {
			// If a CM isn't found or there's an access error, skip it silently.
			// You could log this if you have a logger in Provider.
			continue
		}

		data := make(map[string]string, len(cm.Data)+len(cm.BinaryData))
		for k, v := range cm.Data {
			data[k] = v
		}
		// Binary data -> string (adjust if you need base64 instead)
		for k, b := range cm.BinaryData {
			data[k] = string(b)
		}

		out[name] = data
	}

	return out
}

func buildMountedConfigMaps(pod *v1.Pod, configMaps map[string]map[string]string) ([]virtualpod.FileMapping, error) {
	// Map each volume name -> (configMap name + items map[relativePath]key).
	type cmVolumeDef struct {
		cmName string
		// inside the volume, file "relativePath" comes from ConfigMap key "key"
		items map[string]string // relPath -> key
	}

	volDefs := make(map[string]cmVolumeDef)

	// 1) Collect only ConfigMap-backed volumes
	for _, vol := range pod.Spec.Volumes {
		if vol.ConfigMap == nil {
			continue
		}
		cmName := strings.TrimSpace(vol.ConfigMap.Name)
		if cmName == "" {
			continue
		}

		def := cmVolumeDef{cmName: cmName, items: map[string]string{}}

		// If items are specified, we restrict to those.
		if len(vol.ConfigMap.Items) > 0 {
			for _, it := range vol.ConfigMap.Items {
				relPath := strings.TrimLeft(it.Path, "/") // paths inside volume are relative
				if relPath == "" || it.Key == "" {
					continue
				}
				def.items[relPath] = it.Key
			}
		}

		volDefs[vol.Name] = def
	}

	// 2) Build file mappings from the (single) container's volumeMounts
	var files []virtualpod.FileMapping
	c := pod.Spec.Containers[0]

	for _, vm := range c.VolumeMounts {
		def, ok := volDefs[vm.Name]
		if !ok {
			continue // not a configMap volume (or unknown vol)
		}

		mountDir := strings.TrimRight(vm.MountPath, "/")
		if mountDir == "" {
			continue
		}

		// Case A: subPath -> mount a single file from the volume
		if vm.SubPath != "" {
			rel := strings.TrimLeft(vm.SubPath, "/")
			if rel == "" {
				continue
			}

			key := ""
			if len(def.items) > 0 {
				// subPath must correspond to one of the projected paths
				if k, ok := def.items[rel]; ok {
					key = k
				}
			} else {
				// No items specified → path == key (Kubernetes default behavior)
				key = rel
			}

			if key == "" {
				continue
			}

			// Target path is exactly the mountPath (a single file)
			files = append(files, virtualpod.FileMapping{
				TargetPath:    mountDir,
				ConfigMapName: def.cmName,
				Key:           key,
			})
			continue
		}

		// Case B: entire volume directory
		if len(def.items) > 0 {
			// Use only items declared on the volume
			for rel, key := range def.items {
				files = append(files, virtualpod.FileMapping{
					TargetPath:    path.Join(mountDir, rel),
					ConfigMapName: def.cmName,
					Key:           key,
				})
			}
		} else {
			// No items → include all keys from the ConfigMap
			cmData, ok := configMaps[def.cmName]
			if !ok {
				// If the CM wasn't preloaded, we cannot infer its keys here.
				// Skip silently or log as needed.
				continue
			}
			for key := range cmData {
				files = append(files, virtualpod.FileMapping{
					TargetPath:    path.Join(mountDir, key),
					ConfigMapName: def.cmName,
					Key:           key,
				})
			}
		}
	}

	return files, nil
}
