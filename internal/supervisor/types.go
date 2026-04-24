package supervisor

import (
	"time"

	ctxmgr "github.com/dontfuckmycode/dfmc/internal/context"
)

type TaskState string

const (
	TaskPending        TaskState = "pending"
	TaskRunning        TaskState = "running"
	TaskDone           TaskState = "done"
	TaskBlocked        TaskState = "blocked"
	TaskSkipped        TaskState = "skipped"
	TaskVerifying      TaskState = "verifying"
	TaskWaiting        TaskState = "waiting"
	TaskExternalReview TaskState = "external_review"
)

type WorkerClass string

const (
	WorkerPlanner     WorkerClass = "planner"
	WorkerResearcher  WorkerClass = "researcher"
	WorkerCoder       WorkerClass = "coder"
	WorkerReviewer    WorkerClass = "reviewer"
	WorkerTester      WorkerClass = "tester"
	WorkerSecurity    WorkerClass = "security"
	WorkerSynthesizer WorkerClass = "synthesizer"
	WorkerVerifier    WorkerClass = "verifier"
)

type VerificationStatus string

const (
	VerifyNone     VerificationStatus = "none"
	VerifyLight    VerificationStatus = "light"
	VerifyRequired VerificationStatus = "required"
	VerifyDeep     VerificationStatus = "deep"
)

// Task is the execution-unit shape the future supervisor layer will own.
// It is intentionally richer than drive.Todo so orchestration can carry
// worker intent, verification policy, and routing hints without relying on
// provider prompts alone.
type Task struct {
	ID            string
	ParentID      string
	Origin        string
	RunID         string // drive run that created this task; empty for standalone todos
	Title         string
	Detail        string
	State         TaskState
	DependsOn     []string
	FileScope     []string
	ReadOnly      bool
	ProviderTag   string
	WorkerClass   WorkerClass
	Skills        []string
	AllowedTools  []string
	Labels        []string
	Verification  VerificationStatus
	Confidence    float64
	Summary       string
	Error         string
	BlockedReason string
	Attempts      int
	StartedAt     time.Time
	EndedAt       time.Time
	// LastContext holds the retrieval outcome from the most recent
	// buildContextChunks call. When the task resumes, the same chunks
	// can be reused instead of re-running retrieval from scratch.
	LastContext *ctxmgr.ContextSnapshot
}

type Run struct {
	ID        string
	Task      string
	Status    string
	Reason    string
	CreatedAt time.Time
	EndedAt   time.Time
	Tasks     []Task
}
