import { createFileRoute } from "@tanstack/react-router";
import FailuresPage from "./page";

export const Route = createFileRoute("/failures")({
	component: FailuresPage,
});
