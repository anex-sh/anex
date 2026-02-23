package glami

import (
	"context"
	"errors"
	"io"

	"github.com/virtual-kubelet/virtual-kubelet/log"
	"github.com/virtual-kubelet/virtual-kubelet/node/api"
)

var errNotImplemented = errors.New("operation not implemented for virtual pods")

// RunInContainer executes a command in a container in the pod, copying data
// between in/out/err and the container's stdin/stdout/stderr.
func (p *Provider) RunInContainer(ctx context.Context, namespace, name, container string, cmd []string, attach api.AttachIO) error {
	log.G(ctx).Infof("receive ExecInContainer %q - not implemented for virtual pods", container)

	msg := "Error: exec is not supported for virtual pods. Virtual pods run on external GPU providers and don't support direct shell access."
	writeErrorToStream(attach, msg)

	return nil
}

// AttachToContainer attaches to the executing process of a container in the pod, copying data
// between in/out/err and the container's stdin/stdout/stderr.
func (p *Provider) AttachToContainer(ctx context.Context, namespace, name, container string, attach api.AttachIO) error {
	log.G(ctx).Infof("receive AttachToContainer %q - not implemented for virtual pods", container)

	msg := "Error: attach is not supported for virtual pods. Virtual pods run on external GPU providers and don't support direct attachment."
	writeErrorToStream(attach, msg)

	return nil
}

// PortForward forwards a local port to a port on the pod
func (p *Provider) PortForward(ctx context.Context, namespace, pod string, port int32, stream io.ReadWriteCloser) error {
	log.G(ctx).Infof("receive PortForward %q - not implemented for virtual pods", pod)

	// For port-forward, write to the stream before returning error
	msg := "Error: port-forward is not supported for virtual pods. Use VirtualService CRD to expose virtual pod ports."
	_, _ = stream.Write([]byte(msg))

	return nil
}

// writeErrorToStream writes an error message to the appropriate stream
func writeErrorToStream(attach api.AttachIO, msg string) {
	// If TTY mode, stdout and stderr are merged, so write to stdout
	// Otherwise write to stderr
	if attach.TTY() {
		if out := attach.Stdout(); out != nil {
			_, _ = out.Write([]byte(msg))
		}
	} else {
		if errStream := attach.Stderr(); errStream != nil {
			_, _ = errStream.Write([]byte(msg))
		}
	}
}
