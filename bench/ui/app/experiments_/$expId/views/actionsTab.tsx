import { CompareRefsForm } from "@/components/experiments/compareRefsForm";
import { FollowUpJob } from "@/components/experiments/followUpJob";
import { MigrateForm } from "@/components/experiments/migrateForm";
import { ActionCard } from "@/components/experiments/compareActionCard";
import { Input } from "@/components/ui/input";
import { useGetExperimentAdaptiveQuery } from "@/lib/store/apis/adaptiveApi";
import { useGetSuitesQuery } from "@/lib/store/apis/benchApi";
import {
  EXTEND_MAX_TASKS,
  EXTEND_MAX_TRIALS,
  type ExtendJobParams,
  type ProbeJobParams,
} from "@/lib/types/experimentActions";
import { GitBranch, GitCompareArrows, Layers, Radar } from "lucide-react";
import { useId, useState } from "react";
import { useTranslation } from "react-i18next";

export interface ActionsTabProps {
  expId: string;
  /** The manifest's suite id, and the version it froze. */
  suite?: string;
  suiteVersion?: string;
}

/**
 * The experiment's follow-up actions (web parity L14): extend (more tasks or trials, combined with
 * this analysis), a probe sweep over the suite's fault probes, and a model comparison. Each queues
 * a job that writes, freezes and starts a new experiment, then links it.
 */
export function ActionsTab({ expId, suite, suiteVersion }: ActionsTabProps) {
  const { t } = useTranslation("experimentActions");
  const { t: tu } = useTranslation("compareUx");
  const suiteRef = suite
    ? suiteVersion
      ? `${suite}@${suiteVersion}`
      : suite
    : "";
  return (
    <div className="flex flex-col gap-3" data-testid="experiment-actions">
      <p className="text-muted-foreground text-sm">{tu("actions.intro")}</p>
      <ExtendCard expId={expId} />
      <ProbeCard expId={expId} suite={suite} />
      <ActionCard
        icon={GitCompareArrows}
        title={t("migrate.title")}
        description={t("migrate.description")}
        testId="actions-migrate"
      >
        <MigrateForm suite={suiteRef} testId="actions-migrate-form" />
      </ActionCard>
      <CompareRefsCard suite={suiteRef} />
    </div>
  );
}

function CompareRefsCard({ suite }: { suite: string }) {
  const { t } = useTranslation("refCompare");
  return (
    <ActionCard
      icon={GitBranch}
      title={t("compare.title")}
      description={t("compare.description")}
      testId="actions-compare-refs"
    >
      <CompareRefsForm suite={suite} testId="actions-compare-refs-form" />
    </ActionCard>
  );
}

function clamp(n: number, max: number): number {
  return Math.min(max, Math.max(1, Math.floor(n) || 1));
}

function ExtendCard({ expId }: { expId: string }) {
  const { t } = useTranslation("experimentActions");
  const uid = useId();
  const adaptive = useGetExperimentAdaptiveQuery(expId);
  const [mode, setMode] = useState<"trials" | "tasks">("trials");
  const [count, setCount] = useState(2);
  const [force, setForce] = useState(false);
  const decision = adaptive.data?.decision ?? "";
  const more = adaptive.data?.more_evidence;
  const parent = adaptive.data?.extends;
  const inconclusive = decision.toLowerCase() === "inconclusive";
  const params: ExtendJobParams | null = parent
    ? null
    : {
        exp_id: expId,
        [mode]: clamp(
          count,
          mode === "trials" ? EXTEND_MAX_TRIALS : EXTEND_MAX_TASKS,
        ),
        ...(force ? { force: true } : {}),
      };
  return (
    <ActionCard
      icon={Layers}
      title={t("extend.title")}
      description={t("extend.description")}
      testId="actions-extend"
      defaultOpen={inconclusive && !parent}
      status={
        decision ? (
          <span
            className="bg-muted text-muted-foreground mt-1 w-fit rounded-full px-2 py-0.5 text-xs"
            data-testid="actions-extend-decision"
          >
            {t("extend.decision", { decision })}
          </span>
        ) : undefined
      }
    >
      <div className="flex flex-col gap-3">
        {more && !more.over && (
          <p className="text-muted-foreground text-xs">
            {t("extend.sizing", { tasks: more.tasks, target: more.target_pp })}
          </p>
        )}
        {parent ? (
          <p
            className="text-muted-foreground text-sm"
            data-testid="actions-extend-followup"
          >
            {t("extend.already", { expId: parent.exp_id })}
          </p>
        ) : (
          <>
            <fieldset className="flex flex-wrap gap-4 text-sm">
              <legend className="mb-1 text-sm font-medium">
                {t("extend.mode")}
              </legend>
              <label className="flex items-center gap-2">
                <input
                  type="radio"
                  name={`${uid}-mode`}
                  checked={mode === "trials"}
                  onChange={() => setMode("trials")}
                  data-testid="actions-extend-trials"
                />
                {t("extend.trials")}
              </label>
              <label className="flex items-center gap-2">
                <input
                  type="radio"
                  name={`${uid}-mode`}
                  checked={mode === "tasks"}
                  onChange={() => {
                    setMode("tasks");
                    if (more?.tasks) setCount(more.tasks);
                  }}
                  data-testid="actions-extend-tasks"
                />
                {t("extend.tasks")}
              </label>
            </fieldset>
            <label
              className="flex max-w-40 flex-col gap-1 text-sm"
              htmlFor={`${uid}-count`}
            >
              {t("extend.count")}
              <Input
                id={`${uid}-count`}
                type="number"
                min={1}
                max={mode === "trials" ? EXTEND_MAX_TRIALS : EXTEND_MAX_TASKS}
                value={count}
                onChange={(e) => setCount(Number(e.target.value))}
                data-testid="actions-extend-count"
              />
            </label>
            {decision && !inconclusive && (
              <label className="flex items-center gap-2 text-sm">
                <input
                  type="checkbox"
                  checked={force}
                  onChange={(e) => setForce(e.target.checked)}
                  data-testid="actions-extend-force"
                />
                {t("extend.force")}
              </label>
            )}
          </>
        )}
        <FollowUpJob
          kind="experiment.extend"
          params={
            params && (inconclusive || force || !decision) ? params : null
          }
          label={t("extend.run")}
          confirm={t("extend.confirm", { expId })}
          missing={parent ? undefined : t("extend.notInconclusive")}
          testId="actions-extend-job"
        />
      </div>
    </ActionCard>
  );
}

function ProbeCard({ expId, suite }: { expId: string; suite?: string }) {
  const { t } = useTranslation("experimentActions");
  const uid = useId();
  const suites = useGetSuitesQuery();
  const [picked, setPicked] = useState<string[]>([]);
  const [trials, setTrials] = useState(1);
  const probes = suites.data?.suites.find((s) => s.id === suite)?.probes ?? [];
  const toggle = (id: string) =>
    setPicked((cur) =>
      cur.includes(id) ? cur.filter((x) => x !== id) : [...cur, id],
    );
  const params: ProbeJobParams | null =
    probes.length > 0
      ? {
          exp_id: expId,
          trials: clamp(trials, EXTEND_MAX_TRIALS),
          ...(picked.length ? { probes: picked } : {}),
        }
      : null;
  return (
    <ActionCard
      icon={Radar}
      title={t("probe.title")}
      description={t("probe.description")}
      testId="actions-probe"
    >
      <div className="flex flex-col gap-3">
        {suites.isLoading ? (
          <div
            className="bg-muted h-16 animate-pulse rounded-lg"
            aria-label={t("probe.loading")}
          />
        ) : probes.length === 0 ? (
          <p
            className="text-muted-foreground text-sm"
            data-testid="actions-probe-none"
          >
            {t("probe.none")}
          </p>
        ) : (
          <>
            <fieldset className="flex flex-wrap gap-x-4 gap-y-2 text-sm">
              <legend className="mb-1 text-sm font-medium">
                {t("probe.pick")}
              </legend>
              {probes.map((p) => (
                <label
                  key={p.id}
                  className="hover:bg-muted flex items-center gap-2 rounded-full border px-3 py-1"
                >
                  <input
                    type="checkbox"
                    checked={picked.includes(p.id)}
                    onChange={() => toggle(p.id)}
                    data-testid={`actions-probe-${p.id}`}
                  />
                  <span className="font-mono text-xs">{p.id}</span>
                </label>
              ))}
            </fieldset>
            <label
              className="flex max-w-40 flex-col gap-1 text-sm"
              htmlFor={`${uid}-trials`}
            >
              {t("probe.trials")}
              <Input
                id={`${uid}-trials`}
                type="number"
                min={1}
                max={EXTEND_MAX_TRIALS}
                value={trials}
                onChange={(e) => setTrials(Number(e.target.value))}
              />
            </label>
          </>
        )}
        <FollowUpJob
          kind="probe.run"
          params={params}
          label={t("probe.run")}
          confirm={t("probe.confirm", { expId })}
          testId="actions-probe-job"
        />
      </div>
    </ActionCard>
  );
}

export default ActionsTab;
