import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { useParams } from "@tanstack/react-router";
import { ChecksTab } from "./checksTab";
import { ConfigTab } from "./configTab";
import { ToolsTab } from "./toolsTab";

// Agent detail: Config (lane L08), Checks and Tools (lane L09).
export default function AgentDetailView() {
	const { agentId } = useParams({ strict: false }) as { agentId?: string };
	const id = agentId ?? "";
	return (
		<div className="space-y-4 p-6" data-testid="agent-detail">
			<h1 className="font-mono text-lg font-semibold">{id}</h1>
			<Tabs defaultValue="config">
				<TabsList>
					<TabsTrigger value="config">Config</TabsTrigger>
					<TabsTrigger value="checks" data-testid="agent-tab-checks">
						Checks
					</TabsTrigger>
					<TabsTrigger value="tools" data-testid="agent-tab-tools">
						Tools
					</TabsTrigger>
				</TabsList>
				<TabsContent value="config">
					<ConfigTab agentId={id} />
				</TabsContent>
				<TabsContent value="checks">
					<ChecksTab agentId={id} />
				</TabsContent>
				<TabsContent value="tools">
					<ToolsTab agentId={id} />
				</TabsContent>
			</Tabs>
		</div>
	);
}
