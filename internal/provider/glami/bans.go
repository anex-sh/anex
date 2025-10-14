package glami

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

func (p *Provider) bansPersistenceEnabled() bool {
	return p.config.Provisioning.PersistBansToFile && p.config.Provisioning.BansFilePath != ""
}

// TODO: Allow DB option for this
func (p *Provider) loadMachineBansFromFile() error {
	if !p.bansPersistenceEnabled() {
		return nil
	}
	path := p.config.Provisioning.BansFilePath
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var bans map[string]time.Time
	if err := json.Unmarshal(data, &bans); err != nil {
		return err
	}
	p.mutex.Lock()
	defer p.mutex.Unlock()
	for k, v := range bans {
		p.machineBans[k] = v
	}
	return nil
}

func (p *Provider) persistMachineBansToFile() error {
	if !p.bansPersistenceEnabled() {
		return nil
	}
	path := p.config.Provisioning.BansFilePath
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	p.mutex.RLock()
	bansCopy := make(map[string]time.Time, len(p.machineBans))
	for k, v := range p.machineBans {
		bansCopy[k] = v
	}
	p.mutex.RUnlock()
	data, err := json.MarshalIndent(bansCopy, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
