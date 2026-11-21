import { createFileRoute } from "@tanstack/react-router";
import SharedPage from "./page";

// A share link (lane X14): a read-only page anyone holding the link may open, no sign-in.
export const Route = createFileRoute("/s/$token")({
	component: SharedPage,
});