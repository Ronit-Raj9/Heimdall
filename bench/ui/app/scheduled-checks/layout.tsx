import { createFileRoute } from "@tanstack/react-router";
import ScheduledChecksPage from "./page";

export const Route = createFileRoute("/scheduled-checks")({
	component: ScheduledChecksPage,
});
