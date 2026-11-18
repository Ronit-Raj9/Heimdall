package schemas

// Multi-container tasks (X2, docs/multi-container.md). A task may name a
// docker compose file (Task.Compose). When the suite loads, bench/compose
// parses the supported subset of that file and maps it onto the existing
// model: one compose service becomes the agent's workspace image
// (Task.Environment), every other service a ServiceSpec on the run's private
// network. What a ServiceSpec cannot say on its own is carried in
// ComposeService. The task.yaml schema does not accept these fields: only the
// compose mapping sets them, and they are hashed with the service.

// ComposeService holds the compose settings of one service that the plain
// ServiceSpec has no field for. Every field is optional.
type ComposeService struct {
	// Name is the service's name in the compose file. The ServiceSpec id is
	// that name made a valid host name; when they differ the name is also an
	// alias.
	Name string `json:"name"`
	// Aliases are extra host names the service answers to on the run's
	// network: the compose name, hostname and network aliases.
	Aliases []string `json:"aliases,omitempty"`
	// Entrypoint replaces the image's entrypoint when set (EntrypointSet),
	// as compose's entrypoint does; an empty list with EntrypointSet clears
	// it.
	Entrypoint    []string `json:"entrypoint,omitempty"`
	EntrypointSet bool     `json:"entrypoint_set,omitempty"`
	User          string   `json:"user,omitempty"`
	Workdir       string   `json:"workdir,omitempty"`
	// Tmpfs mounts, each "path" or "path:options".
	Tmpfs []string `json:"tmpfs,omitempty"`
	// Volumes are named volumes of the compose file, one per run, shared
	// between the services that mount the same name, removed with the run.
	Volumes []ComposeVolume `json:"volumes,omitempty"`
	// RunOnce marks a one-shot service (a dependant waits for
	// service_completed_successfully): it must exit 0 before the next
	// service starts, and it is not checked for readiness.
	RunOnce bool `json:"run_once,omitempty"`
	// Verifier lets the verifier join this service's run network while it
	// grades, so it can query the service by name. The network is internal:
	// it has no route out.
	Verifier bool `json:"verifier,omitempty"`
	// Resource caps lowered from the bench defaults (4 GiB, 1024 processes,
	// no CPU cap) by the compose file; zero keeps the default.
	MemoryBytes int64   `json:"memory_bytes,omitempty"`
	CPUs        float64 `json:"cpus,omitempty"`
	Pids        int     `json:"pids,omitempty"`
}

// ComposeVolume is one named volume mount of a compose service.
type ComposeVolume struct {
	Name     string `json:"name"`   // the compose volume name
	Target   string `json:"target"` // absolute container path
	ReadOnly bool   `json:"read_only,omitempty"`
}
