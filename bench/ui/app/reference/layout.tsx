import { createFileRoute } from "@tanstack/react-router";
import ReferencePage from "./page";

// The public reference board (gap G4-03): read-only, anyone may open it, no sign-in.
export const Route = createFileRoute("/reference")({
	component: ReferencePage,
});
