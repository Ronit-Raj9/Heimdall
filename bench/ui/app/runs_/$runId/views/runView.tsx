import { RunCodeScanPanel } from "@/components/codescan/codeScanPanel"; // X4
import { compareSearch } from "@/components/experiments/legacyRoutes";
import { HardwarePanel } from "@/components/gpu/hardwarePanel"; // X7
import { RunReceiptPanel } from "@/components/receipts/runReceiptPanel";
import { RunReviewsBlock } from "@/components/review/runReviewsBlock";
import { RunGatewayPolicy } from "@/components/routing/gatewayPolicyCard"; // W9 routing policy under test
import { AgentsPanel } from "@/components/runs/agents/agentsSection";
import { ClaimVsResult } from "@/components/runs/claimVsResult";
import { GatewayLane } from "@/components/runs/gatewayLane";
import { MetricStrip } from "@/components/runs/metricStrip";
import { OutcomeBadge } from "@/components/runs/outcomeBadge";
import { RunRemoteCoverage } from "@/components/runs/remoteCoverage";
import { RunEvidenceStatus } from "@/components/runs/runEvidenceStatus";
import { RunOutput, runOutputKind } from "@/components/runs/runOutput";
import {
  ServicesList,
  SessionsTimeline,
  ToolCallTable,
} from "@/components/runs/runScenario";
import { stripMetricKinds } from "@/components/runs/runs.utils";
import {
  BlockSkeleton,
  LoadError,
  Section,
} from "@/components/runs/screenStates";
import { RunExportMenu } from "@/components/runs/runExportMenu";
import { RunFailureCard } from "@/components/runs/runFailureCard";
import { RunTraceView } from "@/components/runs/traceView";
import { formatDuration } from "@/components/runs/traceView.utils";
import { ScreenReplay, useRunFrames } from "@/components/runs/screenReplay"; // U08 screen replay
import { RunServiceLogs } from "@/components/services/serviceLogs"; // X2
import { EmptyState } from "@/components/shell/emptyState";
import { EntityLink } from "@/components/shell/entityLink";
import { PageHeader } from "@/components/shell/pageHeader";
import { RouteTabs, type RouteTab } from "@/components/shell/routeTabs";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { GradeSourceTag } from "@/components/verdict/gradeSourceTag";
import { formatDateTime, shortHash } from "@/components/verdict/verdict.utils";
import { RunVisualPanel } from "@/components/visual/runVisualPanel"; // X1 vision grading
import { RunVoicePanel } from "@/components/voice/runVoicePanel"; // X8 voice agents
import i18n, { getLocale } from "@/lib/i18n";
import {
  useGetCatalogQuery,
  useGetRunEventsQuery,
  useGetRunQuery,
} from "@/lib/store/apis/benchApi";
import { useGetRunAgentsQuery } from "@/lib/store/apis/runAgentsApi";
import { errorStatus } from "@/lib/store/apis/session";
import { useGetRunVoiceQuery } from "@/lib/store/apis/voiceApi";
import type { GateResult, RunDetail, Value } from "@/lib/types/bench";
import { Link } from "@tanstack/react-router";
import {
  Coins,
  Footprints,
  GitCompareArrows,
  SearchX,
  Timer,
  Wrench,
  type LucideIcon,
} from "lucide-react";
import { useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { RUN_TABS, hiddenRunTabs, type RunTabId } from "./runTabs";
import { MaximRunButton } from "./maximRunButton";

/**
 * /runs/$runId: one run. The header names its experiment, task, arm and outcome; the tabs hold
 * everything recorded about it (?tab=), and tabs for evidence this run did not record are hidden.
 */
export default function RunView({ runId }: { runId: string }) {
  const { t } = useTranslation("bench");
  const [live, setLive] = useState(false);
  // A live (unsealed) run is polled so new steps arrive and the page flips once it seals.
  const detail = useGetRunQuery(runId, {
    pollingInterval: live ? 2000 : 0,
    skipPollingIfUnfocused: true,
  });
  const sealedNow = detail.data?.sealed;
  if (detail.data && live === Boolean(sealedNow)) setLive(!sealedNow);
  const agents = useGetRunAgentsQuery(runId, { skip: !detail.data });
  const voice = useGetRunVoiceQuery(runId, { skip: !detail.data });
  const { t: tReplay } = useTranslation("replay"); // U08
  const frames = useRunFrames(runId, detail.data ? !detail.data.sealed : false); // U08

  if (detail.isLoading) return <BlockSkeleton rows={8} testId="run-loading" />;
  if (detail.error || !detail.data) {
    if (errorStatus(detail.error) === 404) {
      return (
        <EmptyState
          icon={SearchX}
          message={t("runs.page.notFound", { id: runId })}
          action={{ label: t("runs.page.backToList"), to: "/runs" }}
          testId="run-not-found"
        />
      );
    }
    return (
      <LoadError
        title={t("runs.page.loadError")}
        error={detail.error}
        onRetry={() => void detail.refetch()}
        testId="run-error"
      />
    );
  }

  const d = detail.data;
  const { slot, run } = d;
  const output = runOutputKind(d);
  const hidden = hiddenRunTabs({
    output,
    agents: Boolean(agents.data?.available),
    visual: Boolean(d.grade?.visual),
    voice: Boolean(voice.data),
    gpu: Boolean(run.hardware?.gpus?.length),
    services: Boolean(d.services?.length),
  });
  const tabs: RouteTab[] = RUN_TABS.map((id) => ({
    id,
    label: t(`runs.page.tabs.${id}`),
    hidden: hidden.has(id),
    testId: `run-tab-${id}`,
  }));
  if (frames.data?.frames.length) tabs.splice(2, 0, { id: "replay", label: tReplay("tab"), testId: "run-tab-replay" }); // U08

  return (
    <div
      className="flex min-w-0 flex-col gap-6"
      data-testid="run-screen"
      data-run-id={runId}
      data-output={output}
    >
      <PageHeader
        breadcrumbs={[
          { label: t("experiments.list.title"), to: "/experiments" },
          {
            label: slot.exp_id,
            to: "/experiments/$expId",
            params: { expId: slot.exp_id },
          },
          { label: t("runs.page.crumb", { id: runId }) },
        ]}
        title={t("runs.page.title", {
          task: slot.task,
          arm: slot.arm,
          outcome: d.sealed
            ? t(`runs.outcome.${run.outcome}`)
            : t("runs.list.inFlight"),
        })}
        meta={
          <>
            {d.sealed ? (
              <OutcomeBadge outcome={run.outcome} testId="run-outcome" />
            ) : (
              <span className="text-chart-info-ink text-xs">
                {t("runs.list.inFlight")}
              </span>
            )}
          </>
        }
        subtitle={
          <span
            className="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs"
            data-testid="run-facts"
          >
            <EntityLink kind="run" id={runId} />
            <span>
              {t("runs.page.experiment")}{" "}
              <EntityLink kind="experiment" id={slot.exp_id} />
            </span>
            <span>
              {t("runs.page.task")}{" "}
              <EntityLink kind="task" id={slot.task} suite={slot.suite} />
            </span>
            <span>
              {t("runs.page.slotFacts", {
                condition: slot.condition,
                trial: slot.trial,
              })}
            </span>
          </span>
        }
        actions={
          <div className="flex items-center gap-2">
            <RunExportMenu runId={runId} sealed={Boolean(d.sealed)} />
            <MaximRunButton runId={runId} sealed={Boolean(d.sealed)} />
            {slot.pair_id ? (
              <Button variant="outline" size="sm" asChild>
                <Link
                  to="/compare"
                  search={compareSearch(slot.pair_id, slot.exp_id) as never}
                  data-testid="run-compare"
                >
                  <GitCompareArrows aria-hidden="true" />
                  {t("runs.page.compare")}
                </Link>
              </Button>
            ) : null}
          </div>
        }
      />

      <RouteTabs
        tabs={tabs}
        defaultTab="overview"
        label={t("runs.page.tabsLabel")}
        testId="run-tabs"
      >
        {(active) =>
          active === "replay" ? (
            <ScreenReplay runId={runId} live={!d.sealed} />
          ) : (
            <RunTab tab={active as RunTabId} detail={d} agents={agents.data} />
          )
        }
      </RouteTabs>
    </div>
  );
}

function RunTab({
  tab,
  detail,
  agents,
}: {
  tab: RunTabId;
  detail: RunDetail;
  agents: ReturnType<typeof useGetRunAgentsQuery>["data"];
}) {
  const arm = detail.slot.arm;
  const runId = detail.run.run_id;
  switch (tab) {
    case "overview":
      return <OverviewTab detail={detail} />;
    case "timeline":
      return <TimelineTab detail={detail} />;
    case "output":
      return <RunOutput detail={detail} arm={arm} />;
    case "agents":
      return agents ? <AgentsPanel data={agents} arm={arm} /> : null;
    case "visual":
      return <RunVisualPanel runId={runId} />;
    case "voice":
      return <RunVoicePanel runId={runId} />;
    case "gpu":
      return <HardwarePanel hardware={detail.run.hardware} arm={arm} />;
    case "services":
      return (
        <div className="flex flex-col gap-6">
          {detail.services && (
            <ServicesList services={detail.services} arm={arm} />
          )}
          <RunServiceLogs runId={runId} arm={arm} />
        </div>
      );
    case "security":
      return <RunCodeScanPanel runId={runId} arm={arm} />;
    case "evidence":
      return <EvidenceTab detail={detail} />;
  }
}

function OverviewTab({ detail }: { detail: RunDetail }) {
  const { t } = useTranslation("bench");
  const { t: tt } = useTranslation("runTrace");
  const locale = getLocale();
  const catalog = useGetCatalogQuery();
  const { run, slot, grade } = detail;
  return (
    <div className="flex flex-col gap-8" data-testid="run-overview">
      <RunFailureCard run={run} />
      <div className="flex flex-col gap-4" data-testid="run-summary">
        <div className="flex flex-wrap items-center gap-2">
          {detail.sealed ? (
            <OutcomeBadge outcome={run.outcome} testId="run-summary-outcome" />
          ) : (
            <span className="bg-chart-info/10 text-chart-info-ink inline-flex items-center gap-1.5 rounded-full px-2 py-0.5 text-xs font-medium">
              <span className="bg-chart-info size-1.5 animate-pulse rounded-full" />
              {tt("overview.running")}
            </span>
          )}
          <GradeSourceTag
            source={detail.grade_source}
            judged={Boolean(grade?.judged)}
            testId="run-grade-source"
          />
        </div>
        <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
          <StatCard
            icon={Footprints}
            label={tt("overview.steps")}
            value={metricText(detail.metrics.steps)}
            testId="run-stat-steps"
          />
          <StatCard
            icon={Wrench}
            label={tt("overview.toolCalls")}
            value={metricText(detail.metrics.tool_calls)}
            testId="run-stat-tools"
          />
          <StatCard
            icon={Coins}
            label={tt("overview.cost")}
            value={metricText(detail.metrics.cost_usd, "usd")}
            testId="run-stat-cost"
          />
          <StatCard
            icon={Timer}
            label={tt("overview.time")}
            value={wallText(detail.metrics)}
            testId="run-stat-time"
          />
        </div>
      </div>

      <Section title={tt("overview.agentSaid")} testId="run-grade">
        <ClaimVsResult
          rows={[
            {
              arm: slot.arm,
              run,
              grade: grade ?? undefined,
              gradeState: "ready",
            },
          ]}
        />
      </Section>

      <details className="group rounded-xl border" data-testid="run-details">
        <summary className="text-muted-foreground hover:text-foreground cursor-pointer px-4 py-3 text-sm font-medium">
          {tt("overview.details")}
        </summary>
        <div className="flex flex-col gap-6 border-t px-4 py-4">
          <div data-testid="run-identity">
            <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-1.5 text-xs">
              <Fact label={t("runs.page.facts.arm")}>
                <span className="font-mono font-semibold">{slot.arm}</span>
                {!slot.counted && (
                  <span className="text-muted-foreground ml-2">
                    {t("runs.list.notCounted")}
                  </span>
                )}
              </Fact>
              <Fact label={t("runs.page.facts.agent")}>
                {run.agent.config_id ? (
                  <EntityLink kind="agent" id={run.agent.config_id} />
                ) : null}
                <span className="text-muted-foreground ml-2 font-mono">
                  {run.agent.adapter}
                </span>
              </Fact>
              <Fact label={t("runs.page.facts.suite")}>
                <EntityLink kind="suite" id={slot.suite} />
              </Fact>
              <Fact label={t("runs.page.facts.attempt")}>{run.attempt}</Fact>
              <Fact label={t("runs.page.facts.started")}>
                {formatDateTime(run.timing.started, locale) ?? ""}
              </Fact>
              <Fact label={t("runs.page.facts.sealed")}>
                {run.timing.sealed
                  ? (formatDateTime(run.timing.sealed, locale) ?? "")
                  : t("runs.page.facts.notSealed")}
              </Fact>
              <Fact label={t("runs.page.facts.seal")}>
                <span className="font-mono" title={detail.seal_hash}>
                  {detail.seal_hash
                    ? shortHash(detail.seal_hash)
                    : t("runs.page.facts.notSealed")}
                </span>
                {detail.seal_error && (
                  <span className="text-chart-error-ink ml-2">
                    {t("runs.page.facts.sealMismatch")}
                  </span>
                )}
              </Fact>
            </dl>
          </div>
          <div className="flex flex-col gap-2" data-testid="run-metrics">
            <span className="text-sm font-medium">
              {tt("overview.allMetrics")}
            </span>
            <Card className="py-0 shadow-none">
              <MetricStrip
                lanes={[{ arm: slot.arm, metrics: detail.metrics }]}
                kinds={stripMetricKinds(catalog.data?.metrics)}
              />
            </Card>
          </div>
        </div>
      </details>

      {detail.gates.length > 0 && (
        <Section
          title={t("verdict.gates.title")}
          description={t("runs.page.gatesDescription")}
          testId="run-gates"
          actions={<GradeSourceTag sealedOnly testId="run-gates-source" />}
        >
          <RunGates gates={detail.gates} />
        </Section>
      )}

      <Section
        title={t("review.runs.title")}
        description={t("review.runs.description")}
        testId="run-reviews-section"
      >
        <RunReviewsBlock runId={run.run_id} testId="run-reviews" />
      </Section>
    </div>
  );
}

/** The run's own gate results: which fired, with what effect, and the detail each recorded. */
function RunGates({ gates }: { gates: readonly GateResult[] }) {
  const { t } = useTranslation("bench");
  return (
    <ul
      className="flex flex-col divide-y rounded-sm border text-xs"
      data-testid="run-gate-list"
    >
      {gates.map((gate) => (
        <li
          key={gate.gate}
          className="flex flex-wrap items-baseline gap-x-3 gap-y-1 px-3 py-2"
          data-testid={`run-gate-${gate.gate}`}
          data-fired={gate.fired}
        >
          <span className="font-mono font-medium">{gate.gate}</span>
          <span className="text-muted-foreground">{gate.effect}</span>
          <span
            className={
              gate.fired
                ? "text-chart-error-ink font-medium"
                : "text-chart-success-ink"
            }
          >
            {gate.fired ? t("runs.page.gateFired") : t("runs.page.gateClear")}
          </span>
          {gate.detail && (
            <span className="text-muted-foreground min-w-0 break-words">
              {gate.detail}
            </span>
          )}
        </li>
      ))}
    </ul>
  );
}

/** A metric value as short text, or a dash when it is not known. */
function metricText(value: Value | undefined, unit?: "usd"): string {
  if (!value || typeof value.v !== "number" || value.state !== "observed")
    return unit === "usd" ? String(i18n.t("runs.page.costUnknown" as never, { ns: "bench" } as never)) : "-";
  return unit === "usd"
    ? `$${value.v.toFixed(value.v < 1 ? 4 : 2)}`
    : value.v.toLocaleString();
}

function wallText(metrics: Record<string, Value>): string {
  const ms =
    metrics.wall_ms?.state === "observed" &&
    typeof metrics.wall_ms.v === "number"
      ? metrics.wall_ms.v
      : typeof metrics.wall_s?.v === "number"
        ? metrics.wall_s.v * 1000
        : null;
  return typeof ms === "number" ? formatDuration(ms) : "-";
}

function StatCard({
  icon: Icon,
  label,
  value,
  testId,
}: {
  icon: LucideIcon;
  label: string;
  value: string;
  testId: string;
}) {
  return (
    <div
      className="bg-card flex flex-col gap-1 rounded-xl border p-4 transition-shadow hover:shadow-md"
      data-testid={testId}
    >
      <span className="text-muted-foreground flex items-center gap-1.5 text-xs">
        <Icon className="size-3.5" aria-hidden="true" />
        {label}
      </span>
      <span className="text-2xl font-semibold tracking-tight tabular-nums">
        {value}
      </span>
    </div>
  );
}

function Fact({ label, children }: { label: string; children: ReactNode }) {
  return (
    <>
      <dt className="text-muted-foreground">{label}</dt>
      <dd className="min-w-0 break-words">{children}</dd>
    </>
  );
}

function TimelineTab({ detail }: { detail: RunDetail }) {
  const { t } = useTranslation("bench");
  const arm = detail.slot.arm;
  const runId = detail.run.run_id;
  const events = useGetRunEventsQuery(runId);
  return (
    <div className="flex flex-col gap-8" data-testid="run-timeline">
      <RunRemoteCoverage runId={runId} arm={arm} />
      <RunGatewayPolicy runId={runId} />
      <RunTraceView runId={runId} arm={arm} live={!detail.sealed} />
      {detail.sessions && detail.sessions.length > 0 && (
        <SessionsTimeline sessions={detail.sessions} arm={arm} />
      )}
      {detail.tool_calls && detail.tool_calls.length > 0 && (
        <ToolCallTable calls={detail.tool_calls} arm={arm} />
      )}
      <Section
        title={t("runs.gateway.title")}
        description={t("runs.gateway.description")}
        testId="run-gateway"
      >
        {events.isLoading ? (
          <BlockSkeleton rows={4} testId="run-gateway-loading" />
        ) : events.error || !events.data ? (
          <LoadError
            title={t("runs.gateway.eventsError")}
            error={events.error}
            onRetry={() => void events.refetch()}
            testId="run-gateway-error"
          />
        ) : (
          <CardContent className="rounded-sm border px-4 py-4">
            <GatewayLane
              arm={arm}
              events={events.data.events}
              complete={events.data.complete}
            />
          </CardContent>
        )}
      </Section>
    </div>
  );
}

function EvidenceTab({ detail }: { detail: RunDetail }) {
  const { t } = useTranslation("bench");
  const runId = detail.run.run_id;
  return (
    <div className="flex flex-col gap-8" data-testid="run-evidence">
      <Section
        title={t("runs.evidence.title")}
        description={t("runs.evidence.description")}
        testId="run-evidence-status"
      >
        <Card className="py-0 shadow-none">
          <CardContent className="px-4 py-4">
            <RunEvidenceStatus detail={detail} arm={detail.slot.arm} />
          </CardContent>
        </Card>
      </Section>
      {detail.receipts.length > 0 && (
        <Section
          title={t("receipts.title")}
          description={t("runs.page.receiptsDescription")}
          testId="run-receipts"
        >
          <RunReceiptPanel
            runId={runId}
            probeId={detail.slot.condition}
            onClose={() => undefined}
          />
        </Section>
      )}
    </div>
  );
}
