import { createFileRoute } from "@tanstack/react-router";
import ConnectPage from "./page";

// The Connect box (lane Z01): paste anything, detect, verify, first look.
export const Route = createFileRoute("/connect")({
	component: ConnectPage,
});