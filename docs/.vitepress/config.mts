import { defineConfig } from 'vitepress'

// https://vitepress.dev/reference/site-config
export default defineConfig({
  title: 'Lightyear',
  description: 'Media transcoding platform — Go orchestration, Rust compute core, Nuxt web console and WASM sandbox',
  themeConfig: {
    // https://vitepress.dev/reference/default-theme-config
    nav: [
      { text: '首页', link: '/' },
      { text: '快速开始', link: '/getting-started' },
      { text: '架构', link: '/architecture/overview' },
      { text: '契约', link: '/contracts/' },
      { text: '计算引擎', link: '/compute/color-pipeline' },
      { text: '质量基准', link: '/quality/fidelity-benchmark' },
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
          { text: 'HTTP API', link: '/contracts/http-api' },
          { text: '队列（Redis Streams）', link: '/contracts/queue' },
          { text: 'WASM', link: '/contracts/wasm' }
        ]
      },
      {
        text: '计算引擎',
        items: [
          { text: '编解码器与架构', link: '/compute/codec-engines' },
          { text: '色彩管线', link: '/compute/color-pipeline' },
          { text: '元数据与 Alpha', link: '/compute/metadata' },
          { text: 'HDR Gain Map', link: '/compute/gain-map' },
          { text: '实现审查与取舍', link: '/compute/engineering-review' },
          { text: '路线图', link: '/compute/color-roadmap' },
          { text: '已知限制', link: '/compute/limitations' }
        ]
      },
      {
        text: '质量基准',
        items: [{ text: '保真度基准', link: '/quality/fidelity-benchmark' }]
      },
      {
        text: '工程笔记',
        items: [
          { text: '索引', link: '/engineering-notes/' },
          { text: '输入格式探测', link: '/engineering-notes/input-detection' },
          { text: 'ICC 容器读写', link: '/engineering-notes/icc-containers' },
          { text: '重采样', link: '/engineering-notes/resampling' },
          { text: 'EXIF 元数据', link: '/engineering-notes/exif-metadata' },
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
