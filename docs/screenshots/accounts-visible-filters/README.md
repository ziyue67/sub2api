# 账号筛选常驻显示

> PR 截图已从当前源码中清理，以减小源码包体积。[查看固定提交中的历史截图](https://github.com/ranxi2001/sub2api/tree/297e9201a2f6d6fe70073b370a5b71f49579accf/docs/screenshots/accounts-visible-filters)；下列文件名与验证说明保留用于追溯。

账号管理页使用的 `AccountTableFilters.vue` 本地组件预览，Chrome 151，中文、浅色、空筛选；未连接后端或生产账号。预览仅挂载实际 Vue 组件及项目样式，标题为预览容器。为截图加载本机中文字体，字体不随提交分发。

- `before-desktop.png`：1440 px 宽，production 基线 `c34339596e3de519bc4e755f25e03b368e05b3dc` 的组件，默认折叠。
- `after-desktop.png`：1440 px 宽，本目录所在提交的组件，五项全部显示。
- `after-mobile.png`：375 px 宽，同一修改后组件，五项换行显示。

截图验证组件布局，不代表账号管理整页、真实接口、深色模式或生产环境已验收。
