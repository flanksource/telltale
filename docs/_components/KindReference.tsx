import { Fragment, useState } from 'react';
import catalog from '../_data/kinds.json';
import './kind-reference.css';

type Schema = {
  type?: string;
  description?: string;
  properties?: Record<string, Schema>;
  required?: string[];
  enum?: unknown[];
  default?: unknown;
  items?: Schema;
  additionalProperties?: unknown;
};
type Column = { name: string; type: string; label?: string; description?: string };
type Argument = { name: string; type: string; description: string; default: string; required: boolean };
type Kind = { name: string; params: Schema; columns: Column[]; flags: Argument[]; results?: { name: string; columns: Column[] }[] };

const examples: Record<string, Record<string, unknown>> = {
  exec: { name: 'git', pid: 1234, durationMs: 35, exitCode: 0, stdoutTail: 'main\n' },
  goprof: { takenAt: '2026-01-01T10:00:00Z', pid: 1234, profile: 'heap', sampleType: 'inuse_space', value: 1048576, unit: 'bytes' },
  http: { startedAt: '2026-01-01T10:00:00Z', method: 'GET', host: 'example.com', url: 'https://example.com/', status: 200, durationMs: 34 },
  jvm_jfr: { timestamp: '2026-01-01T10:00:00Z', event: 'cpu', thread: 'main', frame: 'example.Service.run', samples: 3 },
  llm: { phase: 'completed', model: 'example-model', backend: 'example', durationMs: 1200, cost: 0.001 },
  logs: { firstObserved: '2026-01-01T10:00:00Z', count: 1, severity: 'INFO', message: 'Server started', source: 'application' },
  process: { key: '1234@1767261600', pid: 1234, ppid: 1200, name: 'server', command: './server', startedAt: '2026-01-01T10:00:00Z', cwd: '/work/checkout', tags: { owner: 'example' } },
  prometheus: { takenAt: '2026-01-01T10:00:00Z', endpoint: 'http://localhost:8080/metrics', health: 'up', durationMs: 12, samples: 45 },
  query: { time: '2026-01-01T10:00:00Z', profile: 'pod-cpu-by-pod', provider: 'prometheus', row: { pod: 'api-0', value: 0.2 } },
  spans: { traceId: '0123456789abcdef0123456789abcdef', spanId: '0123456789abcdef', service: 'checkout', operation: 'GET /orders', durationMs: 34, status: 'ok' },
  sql: { startedAt: '2026-01-01T10:00:00Z', connection: 'proxy', op: 'SELECT', sql: 'SELECT id FROM orders', durationMs: 8, rows: 2 },
  sql_deadlock: { timestamp: '2026-01-01T10:00:00Z', database: 'orders', victims: 1, participants: 2, victimStatement: 'UPDATE orders SET status = 1' },
  sql_plancache: { sampledAt: '2026-01-01T10:00:00Z', database: 'orders', executions: 5, cpuMs: 25, logicalReads: 100, sql: 'SELECT id FROM orders' },
  sql_top: { sampledAt: '2026-01-01T10:00:00Z', engine: 'postgres', database: 'orders', sessionId: 41, state: 'active', waitType: 'Lock:transactionid', blockedBy: 40, elapsedMs: 1500, sql: 'UPDATE orders SET paid = true' },
  sql_xevent: { timestamp: '2026-01-01T10:00:00Z', name: 'sql_batch_completed', database: 'orders', durationMs: 12, sql: 'SELECT id FROM orders' },
  stacks: { takenAt: '2026-01-01T10:00:00Z', pid: 1234, format: 'goroutine', goroutineId: 1, state: 'running' },
  terminal: { at: '2026-01-01T10:00:00Z', offset: 0, kind: 'output', data: 'Build complete\n', width: 120, height: 40 },
};

function fields(schema: Schema, prefix = ''): Array<{ path: string; schema: Schema; required: boolean }> {
  return Object.entries(schema.properties ?? {}).flatMap(([name, value]) => {
    const path = prefix ? `${prefix}.${name}` : name;
    if (value.properties && Object.keys(value.properties).length) return fields(value, path);
    return [{ path, schema: value, required: (schema.required ?? []).includes(name) }];
  });
}

function valueType(schema: Schema): string {
  if (schema.type === 'array') return `${schema.items?.type ?? 'object'}[]`;
  return schema.type ?? (schema.additionalProperties ? 'object' : 'unknown');
}

function EnumOptions({ values, defaultValue }: { values: unknown[]; defaultValue?: unknown }) {
  const isDefault = (value: unknown) => defaultValue !== undefined && JSON.stringify(value) === JSON.stringify(defaultValue);
  const ordered = [...values.filter(isDefault), ...values.filter((value) => !isDefault(value))];
  return <>{ordered.map((value, index) => <Fragment key={JSON.stringify(value)}>{index > 0 && ' | '}{isDefault(value) ? <strong>{String(value)}</strong> : String(value)}</Fragment>)}</>;
}

function tabsFor(kind: string) {
  const record = examples[kind];
  if (!record) throw new Error(`Missing output example for ${kind}`);
  return record;
}

export function KindReference({ kind }: { kind: string }) {
  const entry = (catalog as Kind[]).find((item) => item.name === kind);
  if (!entry) throw new Error(`Unknown trace kind: ${kind}`);
  const params = fields(entry.params);
  return <>
    <h2>Arguments</h2>
    <p>Options for <code>telltale run {kind}</code>, including common run controls. Enum defaults appear first in bold; <span className="kind-required" aria-label="required">*</span> marks required fields. Global formatting and logging options are listed in <code>--help</code>.</p>
    <div className="kind-table"><table><thead><tr><th>Flag</th><th>Type</th><th>Details</th></tr></thead><tbody>
      {entry.flags.map((flag) => {
        const match = flag.description.match(/\s*\(one of ([^)]+)\)$/);
        const options = match?.[1].split(',').map((value) => value.trim());
        const description = flag.description.replace(/\s*\(one of ([^)]+)\)$/, '').replace(/\s*\(supported seams: \[[^\]]+\]\)$/, '');
        return <tr key={flag.name}><td><code>--{flag.name}</code>{flag.required && <span className="kind-required" aria-label="required">*</span>}</td><td>{options ? <EnumOptions values={options} defaultValue={flag.default} /> : flag.type}</td><td>{description || '—'}{flag.default !== '' && !options?.includes(flag.default) && <> Default: <code>{flag.default}</code>.</>}</td></tr>;
      })}
    </tbody></table></div>
    <details>
    <summary>Capture parameter schema</summary>
    <p>These JSON parameter paths are used when configuring captures and can differ from CLI flag names.</p>
    <div className="kind-table"><table><thead><tr><th>Parameter</th><th>Type</th><th>Details</th></tr></thead><tbody>
      {params.map(({ path, schema, required }) => <tr key={path}><td><code>{path}</code>{required && <span className="kind-required" aria-label="required">*</span>}</td><td>{schema.enum ? <EnumOptions values={schema.enum} defaultValue={schema.default} /> : valueType(schema)}</td><td>{schema.description?.replace(/\s*\(supported seams: \[[^\]]+\]\)$/, '') || '—'}{schema.default !== undefined && !schema.enum?.some((value) => JSON.stringify(value) === JSON.stringify(schema.default)) && <> Default: <code>{JSON.stringify(schema.default)}</code>.</>}</td></tr>)}
    </tbody></table></div>
    </details>
    <h2>Record schema</h2>
    <p>Columns exposed by the kind's record query profile. Fields may be absent or empty depending on the capture.</p>
    <div className="kind-table"><table><thead><tr><th>Field</th><th>Type</th><th>Description</th></tr></thead><tbody>
      {entry.columns.map((column) => <tr key={column.name}><td><code>{column.name}</code></td><td>{column.type}</td><td>{column.description || column.label || '—'}</td></tr>)}
    </tbody></table></div>
    {entry.results?.map((result) => <section key={result.name}>
      <h3>{kind}_{result.name}</h3>
      <p>Read with <code>telltale records {kind} --run &lt;run-id&gt; --result {result.name}</code>.</p>
      <div className="kind-table"><table><thead><tr><th>Field</th><th>Type</th><th>Description</th></tr></thead><tbody>
        {result.columns.map((column) => <tr key={column.name}><td><code>{column.name}</code></td><td>{column.type}</td><td>{column.description || column.label || '—'}</td></tr>)}
      </tbody></table></div>
    </section>)}
    <OutputExamples kind={kind} />
  </>;
}

export function OutputExamples({ kind }: { kind: string }) {
  const [tab, setTab] = useState<'JSON' | 'ANSI' | 'Web'>('JSON');
  const record = tabsFor(kind);
  const entries = Object.entries(record);
  return <section className="kind-output">
    <h2>Example output</h2>
    <p>Illustrative values and selected fields. The ANSI and Web tabs show simplified layouts; actual presentation depends on the capture and formatting options.</p>
    <div role="tablist" aria-label="Example output format">
      {(['JSON', 'ANSI', 'Web'] as const).map((name) => <button key={name} type="button" role="tab" id={`${kind}-${name}-tab`} aria-selected={tab === name} aria-controls={`${kind}-${name}-panel`} tabIndex={tab === name ? 0 : -1} onClick={() => setTab(name)} onKeyDown={(event) => {
        const tabs = ['JSON', 'ANSI', 'Web'] as const;
        const index = tabs.indexOf(name);
        const next = event.key === 'ArrowRight' ? tabs[(index + 1) % tabs.length] : event.key === 'ArrowLeft' ? tabs[(index + tabs.length - 1) % tabs.length] : undefined;
        if (next) { event.preventDefault(); setTab(next); document.getElementById(`${kind}-${next}-tab`)?.focus(); }
      }}>{name}</button>)}
    </div>
    <div role="tabpanel" id={`${kind}-${tab}-panel`} aria-labelledby={`${kind}-${tab}-tab`}>
      {tab === 'JSON' && <pre><code>{JSON.stringify(record, null, 2)}</code></pre>}
      {tab === 'ANSI' && <pre><code>{entries.map(([key, value]) => `${key.padEnd(16)} ${typeof value === 'object' ? JSON.stringify(value) : String(value)}`).join('\n')}</code></pre>}
      {tab === 'Web' && <div className="kind-table"><table><thead><tr><th>Field</th><th>Value</th></tr></thead><tbody>{entries.map(([key, value]) => <tr key={key}><td>{key}</td><td><code>{typeof value === 'object' ? JSON.stringify(value) : String(value)}</code></td></tr>)}</tbody></table></div>}
    </div>
  </section>;
}
