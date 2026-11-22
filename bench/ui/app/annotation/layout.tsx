import { createFileRoute } from "@tanstack/react-router";
import AnnotationPage from "./page";

export const Route = createFileRoute("/annotation")({
	component: AnnotationPage,
});