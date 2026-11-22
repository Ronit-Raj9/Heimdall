package main

// evidenceOnExperimentEnd queues a full mirror pass for an experiment that
// just ended (Z10, INFRA_I4 item 1): the passes after each seal verify only
// the runs sealed since the previous pass, and this one verifies every seal
// and the whole chain again. It never blocks and does nothing when no
// mirror runs.
func evidenceOnExperimentEnd(orgID, expID string) {
	evidenceMu.Lock()
	svc := evidenceMirror
	evidenceMu.Unlock()
	svc.NotifyFull(orgID, expID)
}
