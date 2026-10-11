import { defineConfig } from 'vitepress'

// https://vitepress.dev/reference/site-config
export default defineConfig({
  title: 'Lightyear',
  description: 'Image processing platform — Go orchestration, Rust + libvips worker, Nuxt console with a wasm-vips sandbox',
  themeConfig: {
    // https://vitepress.dev/reference/default-theme-config
    nav: [
      { text: '首页', link: '/' },
      { text: '快速开始', link: '/getting-started' },
      { text: '架构', link: '/architecture/overview' },
      { text: '契约', link: '/contracts/' },
      { text: '处理核心', link: '/compute/imageops' },
      { text: '测试', link: '/quality/fixtures' },
      { text: '工程笔记', link: '/engineering-notes/' },
      { text: '路线图', link: '/roadmap' }
    ],

    sidebar: [
      {
        text: '快速开始',
        items: [
          { text: '安装与运行', link: '/getting-started' },
          { text: '改进路线图', link: '/roadmap' }
        ]
      },
      {
        text: '架构',
        items: [
          { text: '总览与数据模型', link: '/architecture/overview' },
          { text: '请求链路', link: '/architecture/request-flows' },
          { text: '可靠性设计', link: '/architecture/reliability' }
        ]
      },
      {
        text: '接口契约',
        items: [
          { text: '总览与兼容规则', link: '/contracts/' },
          { text: 'ImageOptions', link: '/contracts/image-options' },
          { text: 'HTTP API', link: '/contracts/http-api' },
          { text: '队列（Redis Streams）', link: '/contracts/queue' }
        ]
      },
      {
        text: '处理核心',
        items: [
          { text: 'compute-imageops', link: '/compute/imageops' },
          { text: 'HDR 与已知限制', link: '/compute/limitations' }
        ]
      },
      {
        text: '测试',
        items: [{ text: '素材与测试', link: '/quality/fixtures' }]
      },
      {
        text: '工程笔记',
        items: [
          { text: '索引', link: '/engineering-notes/' },
          { text: 'HDR Gain Map 调研', link: '/engineering-notes/gain-map-research' }
        ]
      }
    ],

    socialLinks: [
      { icon: 'github', link: 'https://github.com/pampas9000/lightyear' }
    ],

    search: {
      provider: 'local'
    }
  }
})
