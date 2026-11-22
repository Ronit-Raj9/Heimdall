package utils

import "github.com/maximhq/bifrost/bench/schemas"

// voiceRTCSecretNames lists the org secrets a voice_rtc block names: the
// LiveKit server's key and secret, the start webhook's token and the SIP
// trunk's credentials (adapters/voicertc.go, voicertc.SecretEnv).
func voiceRTCSecretNames(s *schemas.VoiceRTCAgent) []string {
	if s == nil {
		return nil
	}
	var out []string
	add := func(l *schemas.VoiceRTCLiveKit) {
		if l != nil {
			out = append(out, l.APIKeySecret, l.APISecretSecret, l.StartTokenSecret)
		}
	}
	if s.Mode == schemas.VoiceRTCModeSIP && s.SIP != nil {
		add(s.SIP.LiveKit)
		out = append(out, s.SIP.TrunkUserSecret, s.SIP.TrunkPasswordSecret)
	} else {
		add(s.LiveKit)
	}
	kept := out[:0]
	for _, n := range out {
		if SecretNameOK(n) {
			kept = append(kept, n)
		}
	}
	return kept
}
