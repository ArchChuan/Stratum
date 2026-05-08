# ClawHermes-AI-Go 前端界面

这是一个为 ClawHermes-AI-Go 项目开发的前端界面，用于管理和执行 AI Skills。

## 功能特性

- 技能管理：创建、查看、执行和删除 AI Skills
- 实时执行：直接在界面上执行技能并查看结果
- 响应式设计：支持桌面端和移动端访问
- 用户友好：直观的界面设计和操作流程

## 技术栈

- React.js (Vite)
- Ant Design
- Axios
- React Router

## 快速开始

1. 安装依赖：
```bash
npm install
```

2. 启动开发服务器：
```bash
npm run dev
```

3. 构建生产版本：
```bash
npm run build
```

## API 配置

前端会连接到后端 API，默认地址为 `http://localhost:8080`，可以在 `.env` 文件中修改：

```env
VITE_API_BASE_URL=http://localhost:8080
```

## 项目结构

```
src/
├── components/     # 可复用组件
├── pages/          # 页面组件
├── services/       # API 服务
├── utils/          # 工具函数
├── App.jsx         # 主应用组件
├── main.jsx        # 应用入口
└── index.css       # 全局样式
```

## 开发指南

请遵循以下代码规范：

- 使用 ES6+ 语法
- 组件命名采用帕斯卡命名法
- 文件命名采用烤肉串命名法
- 提交前确保代码通过 ESLint 检查