package model

type HealthResponse struct {
	Status  string `json:"status"`
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Date    string `json:"date"`
}

type GetReposRequest struct {
	Limit int
}

type GetRepoRequest struct {
	Name string
}

type RepoOperationBody struct {
	Kind CommandKind `json:"kind"`
	Ref  string      `json:"ref,omitempty"`
}

type RepoOperationRequest struct {
	Name string
	Body RepoOperationBody
}

type RepoOperationResponse struct {
	Repo RepoState `json:"repo"`
}

type ResetResponse struct {
	Repos []RepoState `json:"repos"`
}
