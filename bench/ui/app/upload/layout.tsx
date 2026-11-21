import { createFileRoute } from "@tanstack/react-router";
import UploadPage from "./page";

export const Route = createFileRoute("/upload")({
	component: UploadPage,
});
