import { Link } from "@tanstack/react-router";

export type ProviderId = "openai" | "anthropic" | "gemini" | "openrouter" | "groq" | "sarvam" | "openai-compatible";

export interface ModelRef {
	provider: ProviderId;
	model: string;
	keyId?: string;
}

export interface ModelPickerProps {
	value: ModelRef | null;
	onChange(v: ModelRef): void;
	purpose?: "agent" | "judge" | "simuser" | "voice-stt" | "voice-tts";
	allowedProviders?: ProviderId[];
	disabled?: boolean;
	showAddKeyLink?: boolean /*default true -> /models?add=<provider>*/;
	testId?: string;
}

export const PROVIDERS: readonly ProviderId[] = ["openai", "anthropic", "gemini", "openrouter", "groq", "sarvam", "openai-compatible"];

/** Test double for components/models/modelPicker: a plain provider select and model input, no store. */
export function ModelPicker({ value, onChange, allowedProviders, disabled, showAddKeyLink = true, testId }: ModelPickerProps) {
	const providers = allowedProviders ?? PROVIDERS;
	const provider = value?.provider ?? providers[0] ?? "openai";
	return (
		<div className="flex flex-wrap items-center gap-2" data-testid={testId ?? "model-picker"}>
			<select
				className="border-input bg-background rounded-md border px-2 py-1 text-sm"
				value={provider}
				disabled={disabled}
				onChange={(e) => onChange({ provider: e.target.value as ProviderId, model: value?.model ?? "" })}
			>
				{providers.map((p) => (
					<option key={p} value={p}>
						{p}
					</option>
				))}
			</select>
			<input
				className="border-input bg-background rounded-md border px-2 py-1 text-sm"
				value={value?.model ?? ""}
				disabled={disabled}
				placeholder="model"
				onChange={(e) => onChange({ provider, model: e.target.value, keyId: value?.keyId })}
			/>
			{showAddKeyLink && (
				<Link to="/models" search={{ add: provider } as never} className="text-primary text-xs underline">
					Add key
				</Link>
			)}
		</div>
	);
}

export default ModelPicker;
