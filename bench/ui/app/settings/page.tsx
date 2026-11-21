import { parseAsString, useQueryState } from "nuqs";
import SettingsView, { DEFAULT_SETTINGS_TAB, settingsTab } from "./views/settingsView";

// Composition only. The screen lives in views/; the tab is ?tab= in the URL.
export default function SettingsPage() {
	const [tab, setTab] = useQueryState("tab", parseAsString);
	return <SettingsView tab={settingsTab(tab)} onTabChange={(next) => void setTab(next === DEFAULT_SETTINGS_TAB ? null : next)} />;
}