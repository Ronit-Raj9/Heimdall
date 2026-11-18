import type { VoiceCall } from "@/lib/types/bench";
import { benchApi } from "./benchApi";

/**
 * X8 voice agents (docs/voice-agents.md): GET /runs/{id}/voice, injected into the bench API so the
 * shared endpoint list stays untouched. A run without a voice caller answers 404.
 */
export const voiceApi = benchApi.injectEndpoints({
	endpoints: (builder) => ({
		getRunVoice: builder.query<VoiceCall, string>({
			query: (runId) => runVoicePath(runId),
		}),
	}),
});

/** The request path of one run's voice call, the id escaped as one path segment. */
export function runVoicePath(runId: string): string {
	return `/runs/${encodeURIComponent(runId)}/voice`;
}

export const { useGetRunVoiceQuery } = voiceApi;
