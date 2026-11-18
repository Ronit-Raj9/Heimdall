import { createFileRoute } from "@tanstack/react-router";
import CalibrationPage from "./page";

// W8 judge calibration (docs/judge-calibration.md).
export const Route = createFileRoute("/calibration")({
	component: CalibrationPage,
});