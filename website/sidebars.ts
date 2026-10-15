import type {SidebarsConfig} from '@docusaurus/plugin-content-docs';

// Canonical IA for the full manual. Sidebar order is the reading order:
// orient -> install -> understand -> do -> reference -> operate -> secure -> fix.

const sidebars: SidebarsConfig = {
  docs: [
    'intro',
    {
      type: 'category',
      label: 'Quickstart',
      link: {type: 'generated-index', title: 'Quickstart', description: 'From zero to a live HTTPS URL.'},
      items: ['quickstart/windows', 'quickstart/docker', 'quickstart/first-deploy'],
    },
    {
      type: 'category',
      label: 'Concepts',
      link: {type: 'generated-index', title: 'Concepts', description: 'How winify thinks: targets, lifecycle, proxy, monitoring.'},
      items: [
        'concepts/architecture',
        'concepts/targets',
        'concepts/lifecycle',
        'concepts/proxy-tls-tunnel',
        'concepts/monitoring',
        'concepts/assistant',
      ],
    },
    {
      type: 'category',
      label: 'How-to',
      link: {type: 'generated-index', title: 'How-to guides', description: 'Task-oriented guides for daily operation.'},
      items: [
        'how-to/add-server',
        'how-to/add-project',
        'how-to/env-variables',
        'how-to/webhooks',
        'how-to/deploy-rollback',
        'how-to/monitoring',
        'how-to/bootstrap',
        'how-to/tunnel-cgnat',
        'how-to/github-app',
        'how-to/notifications',
      ],
    },
    {
      type: 'category',
      label: 'Reference',
      link: {type: 'generated-index', title: 'Reference', description: 'Exact config keys, CLI flags, API routes and schemas.'},
      items: ['reference/config', 'reference/cli', 'reference/api', 'reference/audit'],
    },
    {
      type: 'category',
      label: 'Operations',
      link: {type: 'generated-index', title: 'Operations', description: 'Run winify itself in production.'},
      items: [
        'operations/production-install',
        'operations/backup-restore',
        'operations/upgrade',
        'operations/e2e-checklist',
      ],
    },
    {
      type: 'category',
      label: 'Security',
      link: {type: 'generated-index', title: 'Security', description: 'Credentials, transport trust, web hardening.'},
      items: ['security/credentials', 'security/hardening'],
    },
    {
      type: 'category',
      label: 'Troubleshooting',
      link: {type: 'doc', id: 'troubleshooting/index'},
      items: ['troubleshooting/index', 'troubleshooting/faq'],
    },
  ],
};

export default sidebars;
