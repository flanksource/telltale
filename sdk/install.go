package telltale

import (
	"fmt"
	"sync"
)

type probeInstall struct {
	count   int
	release func() error
}

var installed = struct {
	sync.Mutex
	probes map[string]*probeInstall
}{probes: make(map[string]*probeInstall)}

func (a *Agent) install(probe Probe) (func() error, error) {
	installed.Lock()
	defer installed.Unlock()
	spec, err := probeSpec(probe)
	if err != nil {
		return nil, err
	}
	name := spec.Name
	entry := installed.probes[name]
	if entry == nil {
		entry = &probeInstall{}
		if installer, ok := probe.(Installer); ok {
			var err error
			entry.release, err = installer.Install()
			if err != nil {
				return nil, err
			}
			if entry.release == nil {
				return nil, fmt.Errorf("probe %s install returned no release", name)
			}
		}
		installed.probes[name] = entry
	}
	entry.count++
	return func() error {
		installed.Lock()
		defer installed.Unlock()
		entry.count--
		if entry.count != 0 {
			return nil
		}
		delete(installed.probes, name)
		if entry.release != nil {
			return entry.release()
		}
		return nil
	}, nil
}
