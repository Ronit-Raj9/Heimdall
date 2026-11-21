import { createFileRoute } from "@tanstack/react-router";
import AdminPage from "./page";

export const Route = createFileRoute("/admin")({
	component: AdminPage,
});
