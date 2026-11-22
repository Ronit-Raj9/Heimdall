package adapters

// LauncherSource is the embedded launcher or helper named name
// (remotehttp.py, openaiagent.py, a2a.py, pullagent.py, heimdall_conv.py),
// or "" for an unknown name. Tests of other packages run the launchers
// against real handlers with it.
func LauncherSource(name string) string {
	switch name {
	case "remotehttp.py":
		return remoteLauncherSource
	case "openaiagent.py":
		return openaiLauncherSource
	case "a2a.py":
		return a2aLauncherSource
	case "pullagent.py":
		return pullLauncherSource
	case "heimdall_conv.py":
		return convHelperSource
	}
	return ""
}
