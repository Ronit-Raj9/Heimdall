import { Section } from "@/components/runs/screenStates";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { EyeOff } from "lucide-react";
import { useTranslation } from "react-i18next";
import { ADD_STEPS, ADD_TABS, type AddTab } from "./addYourOwn.steps";
import { CopyCodeBlock } from "./copyCodeBlock";

/**
 * "Add your own": the exact commands of the bring-your-own contract in three tabs, each step a
 * one-line explanation over a copyable block, with the note that hidden material never shows here.
 */
export function AddYourOwnPanel({ defaultTab = "task" }: { defaultTab?: AddTab }) {
	const { t } = useTranslation("bench");
	return (
		<Section title={t("suites.add.title")} description={t("suites.add.description")} testId="suites-add-your-own">
			<p
				role="note"
				className="border-chart-info/50 bg-chart-info/10 text-foreground flex items-start gap-2 rounded-sm border px-3 py-2 text-xs"
				data-testid="suites-add-hidden-note"
			>
				<EyeOff className="text-chart-info-ink mt-0.5 size-3.5 shrink-0" aria-hidden="true" />
				<span>{t("suites.add.hiddenNote")}</span>
			</p>
			<Tabs defaultValue={defaultTab} className="min-w-0">
				<TabsList aria-label={t("suites.add.tabsLabel")} className="h-auto w-full flex-wrap justify-start sm:w-fit">
					{ADD_TABS.map((tab) => (
						<TabsTrigger key={tab} value={tab} className="flex-none" data-testid={`suites-add-tab-${tab}`}>
							{t(`suites.add.tab.${tab}`)}
						</TabsTrigger>
					))}
				</TabsList>
				{ADD_TABS.map((tab) => (
					<TabsContent key={tab} value={tab} className="min-w-0" data-testid={`suites-add-panel-${tab}`}>
						<ol className="flex min-w-0 flex-col gap-3">
							{ADD_STEPS[tab].map((step, index) => {
								const explanation = t(step.labelKey);
								return (
									<li key={step.key} className="flex min-w-0 flex-col gap-1.5">
										<p className="text-foreground text-sm">
											<span className="text-muted-foreground mr-2 font-mono text-xs tabular-nums">{index + 1}.</span>
											{explanation}
										</p>
										<CopyCodeBlock code={step.code} label={explanation} testId={`suites-add-${tab}-${step.key}`} />
									</li>
								);
							})}
						</ol>
					</TabsContent>
				))}
			</Tabs>
		</Section>
	);
}