import { createFileRoute } from "@tanstack/react-router";
import FilesPage from "./page";

export const Route = createFileRoute("/files")({
	component: FilesPage,
});
