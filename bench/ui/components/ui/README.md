# components/ui

Shared design primitives. Tailwind + lucide-react only, no extra dependencies. Every primitive
takes `className` and most take `testId` (rendered as `data-testid`). Strings that live inside a
primitive come from the `ui` i18n namespace (`locales/en/ui.json`).

## Page layout

```tsx
import { PageContainer, PageHeader, Section } from "@/components/ui/section";

<PageContainer>
	<PageHeader title="Runs" subtitle="Every agent run, newest first" actions={<Button>New run</Button>} />
	<Section title="Recent" description="Last 24 hours" actions={<Button variant="ghost">View all</Button>}>
		...
	</Section>
</PageContainer>;
```

- `PageHeader`: 2-4 word title, one-line subtitle, at most one primary action.
- `PageContainer`: centered, max 1200px, `gap-8` between sections.
- `Section`: heading, 2-line-clamped description, actions on the right.

## Status

```tsx
import { StatusPill, StatusDot } from "@/components/ui/statusPill";

<StatusPill tone="ok">Ready</StatusPill>
<StatusPill tone="running">Running</StatusPill>   // dot pulses
<StatusPill tone="warn" dot={false}><AlertTriangle />Check</StatusPill>
```

Tones: `ok` (green), `warn` (amber), `bad` (red), `running` (blue, pulsing), `neutral` (grey).
Keep it to one word and at most 2 pills per card. `Badge` still exists for tags; it is now a soft
pill and gained `info` and `neutral` variants.

## Categories

```tsx
import { CategoryIcon, categoryStyle } from "@/components/ui/categoryIcon";

<CategoryIcon category="computer_use" size="md" />   // soft colored tile
<CategoryIcon category="mcp" bare />                  // icon only
```

Known: coding, terminal, memory, computer_use, browser, voice, support, tool_use (mcp, tools),
research, safety, team (multi_agent), gpu. Spelling is normalized ("Computer Use", "computer-use").
Unknown strings get a neutral box icon. Pass `label` when the icon is the only cue.

## Numbers

```tsx
import { StatCard, StatGrid } from "@/components/ui/statCard";

<StatGrid>
	<StatCard label="Pass rate" value="82%" hint="vs last week" trend={{ value: "+4%", direction: "up" }} />
</StatGrid>;
```

`trend.positive` flips the color when "down" is good (cost, latency).

## Detail on demand

```tsx
import { DetailsDisclosure } from "@/components/ui/detailsDisclosure";
import { KeyValueList } from "@/components/ui/keyValueList";
import { CopyId } from "@/components/ui/copyId";

<DetailsDisclosure meta="3 items">
	<KeyValueList
		items={[
			{ label: "Image digest", value: <CopyId value={digest} display={digest.slice(0, 19)} />, mono: true },
			{ label: "Tool version", value: "1.4.2" },
		]}
	/>
</DetailsDisclosure>;
```

Hashes, paths, lock state and versions belong here, not on the card. For a small muted id use
`<CopyId value={id} className="text-muted-foreground text-xs" />`.

## Empty, error, loading

```tsx
import { EmptyState } from "@/components/shell/emptyState";

<EmptyState icon={PlayCircle} title="No runs yet" body="Start a run to see results here." action={{ label: "New run", to: "/evaluate" }} />;
```

EmptyState lives in `@/components/shell/emptyState`; its props are `title` and `body` (the old
`message` is read as `body`).

`compact` for use inside a card. For failed requests use `LoadError` and for loading use
`BlockSkeleton` from `components/runs/screenStates.tsx`; `Skeleton` is the raw block.

## Lists and tables

```tsx
import { Toolbar, ToolbarSpacer, FilterChips } from "@/components/ui/toolbar";

<Toolbar>
	<FilterChips options={[{ value: "coding", label: "Coding", count: 12, icon: <Code2 /> }]} value={sel} onChange={setSel} multiple />
	<ToolbarSpacer />
	<Input placeholder="Search" />
</Toolbar>;
```

- `Input` width rule: `size="number"` for a short number (a count, a budget), `size="id"` for an
  id or a short name, `size="full"` (the default) for free text and search. Never set a fixed
  pixel width on an input.
- `Card interactive` adds the hover lift for clickable cards. Cards are `rounded-xl` with a soft border.
- `Table` headers are sticky (inside a height-capped container); keep 5-6 columns, right-align
  numbers with `className="text-right"`, put status in a `StatusPill`.