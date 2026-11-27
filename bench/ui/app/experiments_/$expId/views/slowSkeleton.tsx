import { BlockSkeleton, LoadError } from "@/components/runs/screenStates";
import "@/components/experiments/ufrcI18n";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";

/** How long a comparison section may load before it says it could not (Q-206, M7-91). */
export const SECTION_LOAD_TIMEOUT_MS = 8000;

/** The error a section shows after waiting SECTION_LOAD_TIMEOUT_MS: the same shape as a fetch timeout. */
export const SECTION_TIMEOUT_ERROR = { status: "TIMEOUT_ERROR", error: "This part took longer than 8 seconds to load." } as const;

/**
 * A section's loading skeleton that never spins forever (Q-206): after SECTION_LOAD_TIMEOUT_MS it
 * becomes the shared load error, "Could not load this part", with Retry. A new mount starts the
 * wait again, so a Retry that is still slow gets the same time.
 */
export function SlowSkeleton({ rows, testId, onRetry }: { rows: number; testId: string; onRetry: () => void }) {
	const { t } = useTranslation("ufrc");
	const [late, setLate] = useState(false);
	useEffect(() => {
		if (late) return;
		const id = setTimeout(() => setLate(true), SECTION_LOAD_TIMEOUT_MS);
		return () => clearTimeout(id);
	}, [late]);
	if (!late) return <BlockSkeleton rows={rows} testId={testId} />;
	return (
		<LoadError
			title={t("section.slow")}
			error={SECTION_TIMEOUT_ERROR}
			onRetry={() => {
				setLate(false);
				onRetry();
			}}
			testId={`${testId}-slow`}
		/>
	);
}