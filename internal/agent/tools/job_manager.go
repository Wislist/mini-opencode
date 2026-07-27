package tools

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"
)

type JobStatus string

const (
	JobRunning JobStatus = "running"
	JobExited  JobStatus = "exited"
	JobKilled  JobStatus = "killed"
)

type JobSnapshot struct {
	ID        string
	Command   string
	CWD       string
	Status    JobStatus
	Output    string
	ExitCode  int
	Error     string
	StartedAt time.Time
	EndedAt   time.Time
}

type JobManager struct {
	nextID int64
	mu     sync.RWMutex
	jobs   map[string]*Job
}

func NewJobManager() *JobManager {
	return &JobManager{jobs: map[string]*Job{}}
}

func (m *JobManager) Start(command string, cwd string) (*Job, error) {
	if m == nil {
		return nil, fmt.Errorf("job manager is nil")
	}

	id := fmt.Sprintf("shell-%d", atomic.AddInt64(&m.nextID, 1))
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, "bash", "-lc", command)
	cmd.Dir = cwd

	job := &Job{
		id:        id,
		command:   command,
		cwd:       cwd,
		cmd:       cmd,
		cancel:    cancel,
		status:    JobRunning,
		startedAt: time.Now(),
		done:      make(chan struct{}),
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, err
	}

	m.mu.Lock()
	m.jobs[id] = job
	m.mu.Unlock()

	go job.capture(stdout)
	go job.capture(stderr)
	go job.wait()

	return job, nil
}

func (m *JobManager) Get(id string) (*Job, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	job, ok := m.jobs[id]
	return job, ok
}

type Job struct {
	id      string
	command string
	cwd     string
	cmd     *exec.Cmd
	cancel  context.CancelFunc

	mu        sync.RWMutex
	output    bytes.Buffer
	status    JobStatus
	exitCode  int
	err       error
	startedAt time.Time
	endedAt   time.Time
	done      chan struct{}
}

func (j *Job) ID() string { return j.id }

func (j *Job) Done() <-chan struct{} { return j.done }

func (j *Job) Kill() {
	j.mu.Lock()
	if j.status == JobRunning {
		j.status = JobKilled
	}
	j.mu.Unlock()
	j.cancel()
}

func (j *Job) Snapshot() JobSnapshot {
	j.mu.RLock()
	defer j.mu.RUnlock()

	errText := ""
	if j.err != nil {
		errText = j.err.Error()
	}
	return JobSnapshot{
		ID:        j.id,
		Command:   j.command,
		CWD:       j.cwd,
		Status:    j.status,
		Output:    j.output.String(),
		ExitCode:  j.exitCode,
		Error:     errText,
		StartedAt: j.startedAt,
		EndedAt:   j.endedAt,
	}
}

func (j *Job) capture(reader io.Reader) {
	_, _ = io.Copy((*lockedBuffer)(j), reader)
}

func (j *Job) wait() {
	err := j.cmd.Wait()
	exitCode := 0
	if j.cmd.ProcessState != nil {
		exitCode = j.cmd.ProcessState.ExitCode()
	}

	j.mu.Lock()
	if j.status == JobRunning {
		j.status = JobExited
	}
	j.exitCode = exitCode
	j.err = err
	j.endedAt = time.Now()
	j.mu.Unlock()

	close(j.done)
}

type lockedBuffer Job

func (b *lockedBuffer) Write(p []byte) (int, error) {
	j := (*Job)(b)
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.output.Write(p)
}
