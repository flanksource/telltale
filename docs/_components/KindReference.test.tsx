import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import { KindReference } from './KindReference';

describe('trace kind enum choices', () => {
  it('renders the unified process identity and both named result schemas', () => {
    const html = renderToStaticMarkup(<KindReference kind="process" />);
    expect(html).toContain('process_facets');
    expect(html).toContain('process_metrics');
    expect(html).toContain('<code>rssBytes</code>');
    expect(html).toContain('server');
  });

  it('lists defaults first in bold without repeating options in details', () => {
    const html = renderToStaticMarkup(<KindReference kind="http" />);
    expect(html).not.toContain('Allowed values');
    expect(html).not.toContain('(one of connect, mitm)');
    expect(html).toContain('<td><code>--mode</code></td><td><strong>connect</strong> | mitm</td>');
    expect(html).toContain('<td><code>mode</code></td><td><strong>connect</strong> | mitm</td>');
    expect(html).toContain('<td><code>--bodies</code></td><td>string</td>');
    const profile = renderToStaticMarkup(<KindReference kind="goprof" />);
    expect(profile).toContain('<strong>heap</strong> | cpu | allocs | mutex | block | goroutine');
  });

  it('marks required flags and parameters with an accessible asterisk', () => {
    const html = renderToStaticMarkup(<KindReference kind="sql_deadlock" />);
    expect(html).toContain('<code>--connection</code><span class="kind-required" aria-label="required">*</span>');
    expect(html).toContain('<code>connection</code><span class="kind-required" aria-label="required">*</span>');
  });
});
