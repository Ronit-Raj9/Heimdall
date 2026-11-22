import { createFileRoute } from "@tanstack/react-router";
import MaximFailuresPage from "./page";

export const Route = createFileRoute("/maxim")({
	component: MaximFailuresPage,
});
