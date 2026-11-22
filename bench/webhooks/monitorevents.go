package webhooks

// Lane Z22 live monitoring (docs/live-monitors.md): one event type for every
// alert about a monitor incident. Its data carries action (opened, updated
// or resolved) and the incident id, so one incident is one alert thread in
// the receiver (the incident id is a stable dedup key).
const EventMonitorIncident = "monitor.incident"

func init() { Events = append(Events, EventMonitorIncident) } // Z22 live monitoring
