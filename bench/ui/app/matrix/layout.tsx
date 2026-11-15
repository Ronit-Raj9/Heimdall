import { createFileRoute } from "@tanstack/react-router";
import MatrixPage from "./page";

export const Route = createFileRoute("/matrix")({
	component: MatrixPage,
});