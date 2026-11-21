import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import type { SuiteTask } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { ArrowDown, ArrowUp, ArrowUpDown } from "lucide-react";
import { useTranslation } from "react-i18next";
import { AdmissionBadge } from "./suiteBadges";
import { HeldOutBadge, TaskHealthCell } from "./suiteHealth";
import { isHeldOut, isRedacted, type HealthState } from "./suiteHealth.utils";
import { taskLicense, type TaskSort, type TaskSortKey } from "./suites.utils";

export interface SuiteTaskTableProps {
  suiteId: string;
  /** Tasks in the order to show them. */
  rows: readonly SuiteTask[];
  sort: TaskSort;
  onSort: (key: TaskSortKey) => void;
  /** The task whose detail is open, if any. */
  selected: string | null;
  onOpen: (taskId: string) => void;
  /** Suite health; when given, the table gets a health column. */
  health?: HealthState;
  /** Also show the expert columns: kind, skills tested, license and hidden checks. Off by default. */
  moreColumns?: boolean;
}

/**
 * One row per task. Each sortable header is a button with aria-sort; the task id opens its detail.
 * A held-out task the server withholds shows its id and a "held out" badge only, with no detail link.
 */
/** Soft pill colours for the difficulty column (lane U04). */
const DIFFICULTY_PILL: Record<string, string> = {
  easy: "bg-chart-success/10 text-chart-success-ink",
  medium: "bg-chart-warning/10 text-chart-warning-ink",
  hard: "bg-chart-error/10 text-chart-error-ink",
};

export function SuiteTaskTable({
  suiteId,
  rows,
  sort,
  onSort,
  selected,
  onOpen,
  health,
  moreColumns = false,
}: SuiteTaskTableProps) {
  const { t } = useTranslation("bench");
  const head = (
    key: TaskSortKey,
    label: string,
    align: "left" | "right" = "left",
  ) => (
    <SortableHead
      sortKey={key}
      label={label}
      sort={sort}
      onSort={onSort}
      align={align}
    />
  );
  return (
    <Table
      data-testid="suites-task-table"
      aria-label={t("suites.tasks.label", { suite: suiteId })}
      containerClassName="rounded-xl border"
    >
      <TableHeader>
        <TableRow className="hover:bg-transparent">
          {head("id", t("suites.tasks.task"))}
          {moreColumns && head("kind", t("suites.tasks.kind"))}
          {head("difficulty", t("suites.tasks.difficulty"))}
          {moreColumns && (
            <TableHead>{t("suites.tasks.capabilities")}</TableHead>
          )}
          {head("admission", t("suites.tasks.admission"))}
          {moreColumns && head("license", t("suites.tasks.license"))}
          {moreColumns &&
            head("hidden", t("suites.tasks.hiddenTests"), "right")}
          {health && (
            <TableHead data-testid="suites-head-health">
              {t("suiteHealth.column")}
            </TableHead>
          )}
        </TableRow>
      </TableHeader>
      <TableBody>
        {rows.map((task) => {
          if (isRedacted(task))
            return (
              <RedactedRow
                key={task.id}
                task={task}
                health={health}
                moreColumns={moreColumns}
              />
            );
          const license = taskLicense(task);
          const passed = task.checks.filter((check) => check.passed).length;
          return (
            <TableRow
              key={task.id}
              data-testid={`suites-task-row-${task.id}`}
              data-state={selected === task.id ? "selected" : undefined}
              className={cn(selected === task.id && "bg-muted/60")}
            >
              <TableCell className="max-w-80 min-w-48 align-top whitespace-normal">
                <button
                  type="button"
                  onClick={() => onOpen(task.id)}
                  aria-expanded={selected === task.id}
                  aria-label={t("suites.tasks.open", {
                    task: `${task.id}: ${task.title}`,
                  })}
                  className="focus-visible:ring-ring/50 group flex cursor-pointer flex-col items-start gap-0.5 rounded-sm text-left focus-visible:ring-[3px] focus-visible:outline-none"
                  data-testid={`suites-task-open-${task.id}`}
                >
                  <span className="text-foreground text-sm font-medium group-hover:underline">
                    {task.title}
                  </span>
                  <span className="text-muted-foreground font-mono text-xs">
                    {task.id}
                  </span>
                </button>
                {isHeldOut(task) && (
                  <HeldOutBadge
                    task={task}
                    testId={`suites-task-held-out-${task.id}`}
                    className="mt-1"
                  />
                )}
              </TableCell>
              {moreColumns && (
                <TableCell className="align-top font-mono text-xs">
                  {task.kind}
                </TableCell>
              )}
              <TableCell className="align-top text-xs">
                {task.difficulty ? (
                  <span
                    className={cn(
                      "inline-flex rounded-full px-2 py-0.5 text-xs font-medium",
                      DIFFICULTY_PILL[task.difficulty] ??
                        "bg-muted text-muted-foreground",
                    )}
                  >
                    {task.difficulty === "easy" ||
                    task.difficulty === "medium" ||
                    task.difficulty === "hard"
                      ? t(`suites.difficulty.${task.difficulty}`)
                      : task.difficulty}
                  </span>
                ) : (
                  <span className="text-muted-foreground">
                    {t("suites.tasks.unset")}
                  </span>
                )}
              </TableCell>
              {moreColumns && (
                <TableCell className="max-w-64 align-top whitespace-normal">
                  {task.capabilities.length > 0 ? (
                    <span className="flex flex-wrap gap-1">
                      {task.capabilities.map((capability) => (
                        <span
                          key={capability}
                          className="bg-muted text-muted-foreground rounded-sm px-1.5 py-0.5 font-mono text-[11px]"
                        >
                          {capability}
                        </span>
                      ))}
                    </span>
                  ) : (
                    <span className="text-muted-foreground text-xs">
                      {t("suites.tasks.unset")}
                    </span>
                  )}
                </TableCell>
              )}
              <TableCell className="align-top">
                <span className="flex flex-col items-start gap-1">
                  <AdmissionBadge
                    task={task}
                    testId={`suites-task-admission-${task.id}`}
                  />
                  <span className="text-muted-foreground text-[11px] tabular-nums">
                    {task.checks.length > 0
                      ? t("suites.tasks.checksPassed", {
                          passed,
                          total: task.checks.length,
                        })
                      : t("suites.tasks.noChecks")}
                  </span>
                </span>
              </TableCell>
              {moreColumns && (
                <TableCell
                  className="align-top text-xs"
                  data-testid={`suites-task-license-${task.id}`}
                >
                  {license ? (
                    <span className="font-mono">{license}</span>
                  ) : (
                    <span className="text-chart-warning-ink">
                      {t("suites.tasks.noLicense")}
                    </span>
                  )}
                </TableCell>
              )}
              {moreColumns && (
                <TableCell className="text-muted-foreground text-right align-top text-xs tabular-nums">
                  {t("suites.tasks.hiddenTestsCell", {
                    files: task.hidden_test_files,
                    required: task.required_tests,
                  })}
                </TableCell>
              )}
              {health && (
                <TableCell className="max-w-72 align-top whitespace-normal">
                  <TaskHealthCell task={task} health={health} />
                </TableCell>
              )}
            </TableRow>
          );
        })}
      </TableBody>
    </Table>
  );
}

/**
 * A held-out task the server serves by id only: its id and a "held out" badge, no link to a detail
 * the server would refuse, its admission state, and one note where the withheld columns would be.
 */
function RedactedRow({
  task,
  health,
  moreColumns,
}: {
  task: SuiteTask;
  health?: HealthState;
  moreColumns: boolean;
}) {
  const { t } = useTranslation("bench");
  return (
    <TableRow data-testid={`suites-task-row-${task.id}`} data-redacted="true">
      <TableCell className="max-w-80 min-w-48 align-top whitespace-normal">
        <span className="flex flex-col items-start gap-1">
          <span className="text-foreground font-mono text-xs">{task.id}</span>
          <HeldOutBadge
            task={task}
            testId={`suites-task-held-out-${task.id}`}
          />
        </span>
      </TableCell>
      <TableCell
        colSpan={moreColumns ? 3 : 1}
        className="text-muted-foreground align-top text-xs whitespace-normal"
      >
        {t("suiteHealth.heldOutRow")}
      </TableCell>
      <TableCell className="align-top">
        <AdmissionBadge
          task={task}
          testId={`suites-task-admission-${task.id}`}
        />
      </TableCell>
      {moreColumns && (
        <TableCell
          colSpan={2}
          className="text-muted-foreground align-top text-xs"
        >
          {t("suiteHealth.withheld")}
        </TableCell>
      )}
      {health && (
        <TableCell className="max-w-72 align-top whitespace-normal">
          <TaskHealthCell task={task} health={health} />
        </TableCell>
      )}
    </TableRow>
  );
}

interface SortableHeadProps {
  sortKey: TaskSortKey;
  label: string;
  sort: TaskSort;
  onSort: (key: TaskSortKey) => void;
  align: "left" | "right";
}

/** A header that sorts its column: a real button, so it is reachable and operable by keyboard. */
function SortableHead({
  sortKey,
  label,
  sort,
  onSort,
  align,
}: SortableHeadProps) {
  const { t } = useTranslation("bench");
  const active = sort.key === sortKey;
  const Icon = !active ? ArrowUpDown : sort.dir === "asc" ? ArrowUp : ArrowDown;
  const name = t("suites.tasks.sortBy", { column: label });
  return (
    <TableHead
      aria-sort={
        active ? (sort.dir === "asc" ? "ascending" : "descending") : "none"
      }
      className={cn(align === "right" && "text-right")}
      data-testid={`suites-head-${sortKey}`}
    >
      <button
        type="button"
        onClick={() => onSort(sortKey)}
        aria-label={
          active
            ? `${name}, ${t(sort.dir === "asc" ? "suites.tasks.sortedAsc" : "suites.tasks.sortedDesc")}`
            : name
        }
        className={cn(
          "focus-visible:ring-ring/50 -mx-1 inline-flex cursor-pointer items-center gap-1 rounded-sm px-1 py-0.5 font-medium transition-colors focus-visible:ring-[3px] focus-visible:outline-none",
          active
            ? "text-foreground"
            : "text-muted-foreground hover:text-foreground",
          align === "right" && "flex-row-reverse",
        )}
        data-testid={`suites-sort-${sortKey}`}
      >
        <span>{label}</span>
        <Icon
          className={cn("size-3.5 shrink-0", !active && "opacity-50")}
          aria-hidden="true"
        />
      </button>
    </TableHead>
  );
}
