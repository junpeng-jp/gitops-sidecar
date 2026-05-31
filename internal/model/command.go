package model

// CommandKind is the JSON discriminator for POST /repos/{name}/operation.
type CommandKind string

const (
	PullKind CommandKind = "pull"
)

// Command is a sealed interface; only types in this package satisfy it.
type Command interface {
	isCommand()
	RepoName() string
}

// InitCommand is an internal-only command enqueued at startup and on reset. Not exposed via API.
type InitCommand struct {
	Name string
}

func (InitCommand) isCommand()         {}
func (c InitCommand) RepoName() string { return c.Name }

// PullCommand fetches and atomically checks out a ref for the named repo.
type PullCommand struct {
	Name string
	Ref  string
}

func (PullCommand) isCommand()         {}
func (c PullCommand) RepoName() string { return c.Name }
