//go:build !linux

package nvme

func IdentifyController(string) (Controller, error)       { return Controller{}, ErrUnsupported }
func IdentifyNamespace(string, uint32) (Namespace, error) { return Namespace{}, ErrUnsupported }
