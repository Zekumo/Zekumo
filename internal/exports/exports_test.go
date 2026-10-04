package exports

import (
	"context"
	"testing"
	"time"

	"minicloud/internal/repo"
)

type timeoutJobs struct {
	failed       chan struct{}
	failedCtxErr error
	failedMsg    string
}

func (j *timeoutJobs) Create(context.Context, string, string, *string, string) (*repo.ExportJob, error) {
	panic("not used")
}

func (j *timeoutJobs) ByID(context.Context, string) (*repo.ExportJob, error) {
	return &repo.ExportJob{ID: "job-timeout", GameID: "game-1", Format: "json"}, nil
}

func (j *timeoutJobs) ByGame(context.Context, string, int, int) ([]repo.ExportJob, error) {
	panic("not used")
}

func (j *timeoutJobs) SetRunning(context.Context, string) error { return nil }
func (j *timeoutJobs) SetDone(context.Context, string, string, int64) error {
	panic("not used")
}

func (j *timeoutJobs) SetFailed(ctx context.Context, _ string, message string) error {
	j.failedCtxErr = ctx.Err()
	j.failedMsg = message
	close(j.failed)
	return nil
}

func (j *timeoutJobs) FailStale(context.Context) error { return nil }

func TestRunMarksTimedOutJobFailedWithFreshContext(t *testing.T) {
	jobs := &timeoutJobs{failed: make(chan struct{})}
	svc := &Service{
		Jobs:        jobs,
		workTimeout: 10 * time.Millisecond,
		buildExport: func(ctx context.Context, _ *repo.ExportJob) ([]byte, string, error) {
			<-ctx.Done()
			return nil, "", ctx.Err()
		},
	}

	svc.run("job-timeout")
	select {
	case <-jobs.failed:
	case <-time.After(time.Second):
		t.Fatal("timed-out export never reached failed state")
	}
	if jobs.failedCtxErr != nil {
		t.Fatalf("SetFailed received canceled context: %v", jobs.failedCtxErr)
	}
	if jobs.failedMsg != context.DeadlineExceeded.Error() {
		t.Fatalf("failure message = %q, want deadline exceeded", jobs.failedMsg)
	}
}
