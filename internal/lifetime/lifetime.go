// Package lifetime runs the services that last as long as telltale serves,
// such as its HTTP and OTLP servers and the rules loops, outside clicky's task
// workers. A task holds a worker for as long as it runs, so a few of these
// held every worker and a later one, the HTTP server among them, never started.
package lifetime

import "github.com/flanksource/clicky/task"

// Service is a function running on its own goroutine, listed as a clicky
// managed run that ends with it.
type Service struct {
	run  *task.ManagedRun
	done chan struct{}
	err  error
}

// Start runs fn as name until it returns.
func Start(name string, fn func() error) *Service {
	service := &Service{run: task.StartManagedRun(name), done: make(chan struct{})}
	service.run.SetBackground(true)
	go func() {
		defer close(service.done)
		service.err = fn()
		status := task.StatusSuccess
		if service.err != nil {
			status = task.StatusFailed
		}
		service.run.Finish(status, service.err)
	}()
	return service
}

// Done is closed once fn returned.
func (s *Service) Done() <-chan struct{} { return s.done }

// Wait is fn's error, once it returned.
func (s *Service) Wait() error {
	<-s.done
	return s.err
}
