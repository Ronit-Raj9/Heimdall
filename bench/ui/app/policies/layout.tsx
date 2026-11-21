import { createFileRoute } from "@tanstack/react-router";
import PoliciesPage from "./page";

export const Route = createFileRoute("/policies")({
	component: PoliciesPage,
});
