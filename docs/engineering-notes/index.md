# 工程笔记

这里存放**迁移记录与研究笔记**：它们记录“为什么这样做”与“当时比对了什么”，不是现状规范。现状请看[架构](/architecture/overview)、[契约](/contracts/)与[色彩管线](/compute/color-pipeline)。

## 计算模块可用库评估（已完成 4 项）

围绕“成熟容器/算法问题优先用库，不手写”的原则，按顺序完成了 4 项迁移：

| # | 笔记 | 迁移 | 状态 |
| :--- | :--- | :--- | :--- |
| 1 | [输入格式探测](./input-detection) | 扩展名/声明 → magic bytes（`infer`） | ✅ |
| 2 | [ICC 容器读写](./icc-containers) | 手写字节扫描 → `img-parts` | ✅ |
| 3 | [重采样](./resampling) | `image::imageops` → `fast_image_resize` | ✅ |
| 4 | [EXIF 元数据](./exif-metadata) | 手写 TIFF/容器解析 → `kamadak-exif` | ✅ |

后续候选（尚未排期）：`moxcms`（ICC/CICP 转换，当前刻意省略）、`mp4parse`（ISOBMFF `colr`/`auxC`，可移除本地盒子解析）。

## 调研笔记

| 笔记 | 主题 |
| :--- | :--- |
| [HDR Gain Map 直通](./gain-map-research) | 通用工具链为何无法保留 Gain Map，以及正确的容器级架构定位 |

## 写作约定

- 记录**决策依据**与**行为差异**（迁移前/后对照），不复述现状
- 结论若已被后续重构推翻，直接修改本文档，不做“叠加更正”
- 状态矩阵里的 ✅ 判定标准是**存在调用方**，不是“存在代码”
