import { createFileRoute } from "@tanstack/react-router";
import MigrationPage from "./page";

export const Route = createFileRoute("/migration")({
	component: MigrationPage,
});