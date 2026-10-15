import {themes as prismThemes} from 'prism-react-renderer';
import type {Config} from '@docusaurus/types';
import type * as Preset from '@docusaurus/preset-classic';

// DevOps Control Center (winify) — public documentation site.
// Deployed via .github/workflows/docs.yml to GitHub Pages.
// Keep onBrokenLinks=throw: production docs must never ship dead links.

const config: Config = {
  title: 'DevOps Control Center',
  tagline: 'Self-hosted deployments for Windows and Docker — git push to live HTTPS URL.',
  favicon: 'img/favicon.svg',

  // Production URL: custom domain winify.space (CNAME in static/).
  url: 'https://winify.space',
  baseUrl: '/',
  organizationName: 'Devonlegend',
  projectName: 'winify',

  onBrokenLinks: 'throw',
  onBrokenMarkdownLinks: 'throw',
  trailingSlash: false,

  i18n: {
    defaultLocale: 'en',
    locales: ['en'],
  },

  markdown: {
    mermaid: true,
  },
  themes: [
    '@docusaurus/theme-mermaid',
    [
      require.resolve('@easyops-cn/docusaurus-search-local'),
      {
        hashed: true,
        language: ['en'],
        highlightSearchTermsOnTargetPage: true,
        explicitSearchResultPath: true,
        docsRouteBasePath: '/',
      },
    ],
  ],

  presets: [
    [
      'classic',
      {
        docs: {
          sidebarPath: './sidebars.ts',
          routeBasePath: '/',
          editUrl: 'https://github.com/Devonlegend/winify/tree/main/website/',
          showLastUpdateTime: true,
          showLastUpdateAuthor: false,
          breadcrumbs: true,
        },
        blog: false,
        theme: {
          customCss: './src/css/custom.css',
        },
        sitemap: {
          changefreq: 'weekly',
          priority: 0.5,
          filename: 'sitemap.xml',
        },
      } satisfies Preset.Options,
    ],
  ],

  themeConfig: {
    image: 'img/logo-dark.svg',
    metadata: [
      {name: 'description', content: 'Self-hosted deployment platform for Windows IIS/services and Docker. Owns build, deploy, reverse proxy, TLS, monitoring and audit.'},
      {name: 'og:type', content: 'website'},
    ],
    colorMode: {
      defaultMode: 'dark',
      disableSwitch: false,
      respectPrefersColorScheme: true,
    },
    navbar: {
      title: 'DevOps Control Center',
      logo: {
        alt: 'DevOps Control Center logo',
        src: 'img/logo-dark.svg',
      },
      items: [
        {
          type: 'docSidebar',
          sidebarId: 'docs',
          position: 'left',
          label: 'Docs',
        },
        {
          to: '/reference/api',
          label: 'API',
          position: 'left',
        },
        {
          to: '/troubleshooting/',
          label: 'Troubleshooting',
          position: 'left',
        },
        {
          href: 'https://github.com/Devonlegend/winify',
          label: 'GitHub',
          position: 'right',
        },
      ],
    },
    footer: {
      style: 'dark',
      links: [
        {
          title: 'Docs',
          items: [
            {label: 'Introduction', to: '/intro'},
            {label: 'Quickstart', to: '/quickstart/first-deploy'},
            {label: 'Concepts', to: '/concepts/architecture'},
          ],
        },
        {
          title: 'Reference',
          items: [
            {label: 'Configuration', to: '/reference/config'},
            {label: 'CLI', to: '/reference/cli'},
            {label: 'REST API', to: '/reference/api'},
          ],
        },
        {
          title: 'Project',
          items: [
            {label: 'GitHub', href: 'https://github.com/Devonlegend/winify'},
            {label: 'Security', to: '/security/credentials'},
            {label: 'Troubleshooting', to: '/troubleshooting/'},
          ],
        },
      ],
      copyright: `DevOps Control Center (winify). Self-hosted deployment platform.`,
    },
    prism: {
      theme: prismThemes.github,
      darkTheme: prismThemes.dracula,
      additionalLanguages: ['powershell', 'bash', 'yaml', 'json', 'go', 'docker'],
    },
    mermaid: {
      theme: {light: 'neutral', dark: 'dark'},
    },
  } satisfies Preset.ThemeConfig,
};

export default config;
