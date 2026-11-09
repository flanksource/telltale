import { defineConfig } from '@rspress/core';

export default defineConfig({
  root: '.',
  title: 'Telltale',
  description: 'Trace applications and inspect the processes running on your host.',
  outDir: 'doc_build',
  route: {
    extensions: ['.md', '.mdx'],
    exclude: ['plans/**', 'doc_build/**'],
  },
  builderConfig: {
    plugins: [{
      name: 'telltale-docs-watch',
      setup(api) {
        api.modifyRsbuildConfig((config) => {
          const watches = config.dev?.watchFiles;
          if (!watches) return;
          for (const watch of Array.isArray(watches) ? watches : [watches]) {
            if (watch.type !== 'restart') continue;
            const ignored = watch.options?.ignored;
            watch.options = {
              ...watch.options,
              ignored: [
                ...(ignored ? (Array.isArray(ignored) ? ignored : [ignored]) : []),
                /(^|[/\\])(node_modules|\.git|\.rspress|doc_build|plans)([/\\]|$)/,
              ],
            };
          }
        });
      },
    }],
  },
  themeConfig: {
    nav: [
      { text: 'Guide', link: '/getting-started' },
      { text: 'GitHub', link: 'https://github.com/flanksource/telltale' },
    ],
    sidebar: {
      '/': [
        {
          text: 'Get started',
          items: [
            { text: 'Introduction', link: '/' },
            { text: 'Quick start', link: '/getting-started' },
          ],
        },
        {
          text: 'Guides',
          items: [
            { text: 'Web workspace', link: '/web-ui' },
            { text: 'OpenTelemetry receiver', link: '/otlp-receiver' },
            { text: 'Resource monitoring rules', link: '/rules' },
            { text: 'SQL Server and JVM captures', link: '/sqlserver-jvm' },
          ],
        },
        {
          text: 'Trace kinds',
          items: [
            { text: 'Overview', link: '/traces/' },
            ...['exec', 'goprof', 'http', 'jvm_jfr', 'llm', 'logs', 'process', 'prometheus', 'query', 'spans', 'sql', 'sql_deadlock', 'sql_plancache', 'sql_top', 'sql_xevent', 'stacks', 'terminal'].map((kind) => ({ text: kind, link: `/traces/${kind}` })),
          ],
        },
      ],
    },
  },
});
