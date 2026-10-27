package groups

import "fmt"

// Applications attributes helpers to their nearest application bundle ancestor.
func Applications(processes []ProcessSample) (map[string]string, error) {
	byPID := map[int]ProcessSample{}
	for _, p := range processes {
		if _, exists := byPID[p.PID]; exists {
			return nil, fmt.Errorf("duplicate process PID %d", p.PID)
		}
		byPID[p.PID] = p
	}
	resolved := map[int]string{}
	visiting := map[int]bool{}
	var resolve func(int) (string, error)
	resolve = func(pid int) (string, error) {
		if value, ok := resolved[pid]; ok {
			return value, nil
		}
		p, ok := byPID[pid]
		if !ok {
			return "", nil
		}
		if visiting[pid] {
			return "", fmt.Errorf("application ancestry cycle at PID %d", pid)
		}
		visiting[pid] = true
		app, _ := ApplicationOf(p.Command)
		if app == "" && p.PPID != pid {
			var err error
			app, err = resolve(p.PPID)
			if err != nil {
				return "", err
			}
		}
		visiting[pid] = false
		resolved[pid] = app
		return app, nil
	}
	result := map[string]string{}
	for _, p := range processes {
		app, err := resolve(p.PID)
		if err != nil {
			return nil, err
		}
		result[p.Key] = app
	}
	return result, nil
}
