import type {PrismTheme} from 'prism-react-renderer';
import type {Config} from '@docusaurus/types';
import type * as Preset from '@docusaurus/preset-classic';

// Winify — public documentation site.
// Deployed via .github/workflows/docs.yml to GitHub Pages.
// Keep onBrokenLinks=throw: production docs must never ship dead links.
//
// The docs are a *Read* surface and mirror the product's monochrome operator
// console (DESIGN.md): warm-black surfaces, light-ink emphasis, hairline
// borders, IBM Plex + Newsreader. Tokens live in src/css/custom.css.

// Fonts: the same three families the product loads, with a full local fallback
// stack in custom.css so an offline host still renders cleanly. The docs add
// IBM Plex Sans 700 / Mono 600 for headings and code keywords.
const fontStylesheet =
  'https://fonts.googleapis.com/css2?family=IBM+Plex+Mono:wght@400;500;600&family=IBM+Plex+Sans:wght@400;500;600;700&family=Newsreader:ital,opsz,wght@0,6..72,400;0,6..72,500;1,6..72,400&display=swap';

// Code theme: monochrome by value and weight, with a single warm tint reserved
// for string literals so they stay scannable without introducing a brand hue.
const codeInk: PrismTheme = {
  plain: {
    color: '#d9d5cd',
    backgroundColor: '#0f0f0e',
  },
  styles: [
    {
      types: ['comment', 'prolog', 'doctype', 'cdata', 'blockquote'],
      style: {color: '#6f6c65', fontStyle: 'italic'},
    },
    {types: ['punctuation'], style: {color: '#7d7973'}},
    {types: ['operator', 'entity', 'url'], style: {color: '#a8a49d'}},
    {
      types: ['keyword', 'builtin', 'atrule', 'important', 'rule'],
      style: {color: '#f4f1ec', fontWeight: '600'},
    },
    {types: ['class-name', 'maybe-class-name'], style: {color: '#f4f1ec'}},
    {types: ['function', 'method'], style: {color: '#ece9e4'}},
    {types: ['string', 'char', 'attr-value', 'inserted'], style: {color: '#d8c9a6'}},
    {types: ['number', 'boolean', 'constant', 'symbol', 'regex'], style: {color: '#c9c4bb'}},
    {types: ['variable', 'attr-name', 'property', 'tag', 'selector'], style: {color: '#dedad3'}},
    {types: ['namespace'], style: {opacity: 0.7}},
    {types: ['deleted'], style: {color: '#f2efe9', textDecorationLine: 'line-through'}},
    {types: ['bold'], style: {fontWeight: '600'}},
    {types: ['italic'], style: {fontStyle: 'italic'}},
  ],
};

const codeInkLight: PrismTheme = {
  plain: {
    color: '#2a2825',
    backgroundColor: '#f4f2ee',
  },
  styles: [
    {
      types: ['comment', 'prolog', 'doctype', 'cdata', 'blockquote'],
      style: {color: '#8a857d', fontStyle: 'italic'},
    },
    {types: ['punctuation'], style: {color: '#8a857d'}},
    {types: ['operator', 'entity', 'url'], style: {color: '#57534c'}},
    {
      types: ['keyword', 'builtin', 'atrule', 'important', 'rule'],
      style: {color: '#141311', fontWeight: '600'},
    },
    {types: ['class-name', 'maybe-class-name'], style: {color: '#141311'}},
    {types: ['function', 'method'], style: {color: '#1a1917'}},
    {types: ['string', 'char', 'attr-value', 'inserted'], style: {color: '#6b5320'}},
    {types: ['number', 'boolean', 'constant', 'symbol', 'regex'], style: {color: '#4a463f'}},
    {types: ['variable', 'attr-name', 'property', 'tag', 'selector'], style: {color: '#33312c'}},
    {types: ['namespace'], style: {opacity: 0.7}},
    {types: ['deleted'], style: {color: '#141311', textDecorationLine: 'line-through'}},
    {types: ['bold'], style: {fontWeight: '600'}},
    {types: ['italic'], style: {fontStyle: 'italic'}},
  ],
};

const config: Config = {
  title: 'Winify',
  tagline: 'Self-hosted deployments for Windows and Docker: git push to live HTTPS URL.',
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

  // Browser chrome follows the console ground in each color mode.
  headTags: [
    {
      tagName: 'link',
      attributes: {rel: 'preconnect', href: 'https://fonts.googleapis.com'},
    },
    {
      tagName: 'link',
      attributes: {
        rel: 'preconnect',
        href: 'https://fonts.gstatic.com',
        crossorigin: 'anonymous',
      },
    },
    {
      tagName: 'meta',
      attributes: {name: 'theme-color', content: '#0a0a09'},
    },
  ],

  stylesheets: [fontStylesheet],

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
    image: 'img/logo-light.svg',
    // Long-form reading: let the operator collapse the sidebar to widen the
    // column they are reading.
    docs: {
      sidebar: {
        hideable: true,
        autoCollapseCategories: false,
      },
    },
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
      title: 'Winify',
      logo: {
        alt: 'Winify logo',
        // Dark ink on the light ground; light ink on the black console.
        src: 'img/logo-light.svg',
        srcDark: 'img/logo-dark.svg',
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
      copyright: `Winify. Self-hosted deployment platform.`,
    },
    prism: {
      theme: codeInkLight,
      darkTheme: codeInk,
      additionalLanguages: ['powershell', 'bash', 'yaml', 'json', 'go', 'docker'],
    },
    mermaid: {
      // Mermaid's built-in neutral/dark themes are already grayscale; the
      // monochrome console treatment is applied to the container in custom.css.
      theme: {light: 'neutral', dark: 'dark'},
      options: {
        fontFamily: '"IBM Plex Sans", system-ui, -apple-system, "Segoe UI", sans-serif',
      },
    },
  } satisfies Preset.ThemeConfig,
};

export default config;
