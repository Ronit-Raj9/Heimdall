import { calibrationRedirect } from "@/lib/routing/u3Redirects";
import { createFileRoute, redirect } from "@tanstack/react-router";

// UI regroup (U3): judge calibration is the Calibration tab of /suites/$suiteId.
export const Route = createFileRoute("/calibration")({
	beforeLoad: ({ search }) => {
		throw redirect({ ...calibrationRedirect(search as Record<string, unknown>), replace: true } as never);
	},
});