export { knowledgeApi } from './api/knowledge.api';
export { useKnowledgePage } from './hooks/useKnowledgePage';
export { useKnowledgeDetailPage } from './hooks/useKnowledgeDetailPage';
export { KnowledgePage } from './pages/KnowledgePage';
export { KnowledgeDetailPage } from './pages/KnowledgeDetailPage';
export { knowledgeRoutes } from './routes';
// 跨模块共享组件：chat 来源卡片经此预览原文（P1.4）
export { DocPreviewDrawer } from './components/DocPreviewDrawer';
export type {
  Workspace,
  WorkspaceConfig,
  WorkspaceStats,
  QuerySource,
  QueryResult,
  CreateWorkspaceInput,
} from './model/knowledge';
