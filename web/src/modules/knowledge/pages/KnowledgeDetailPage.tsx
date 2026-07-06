import { WorkspaceConfigForm } from '../components/WorkspaceConfigForm';
import { WorkspaceDetailHeader } from '../components/WorkspaceDetailHeader';
import { WorkspaceDetailSkeleton } from '../components/WorkspaceDetailSkeleton';
import { WorkspaceDocumentsTable } from '../components/WorkspaceDocumentsTable';
import { WorkspaceQueryPanel } from '../components/WorkspaceQueryPanel';
import { WorkspaceStatsCard } from '../components/WorkspaceStatsCard';
import { WorkspaceUploadZone } from '../components/WorkspaceUploadZone';
import { useKnowledgeDetailPage } from '../hooks/useKnowledgeDetailPage';

export const KnowledgeDetailPage = () => {
  const {
    name,
    navigate,
    isAdmin,
    stats,
    statsLoading,
    configForm,
    configLoading,
    uploadLoading,
    queryForm,
    queryLoading,
    queryResult,
    documents,
    documentsLoading,
    handleConfigSave,
    handleDescriptionSave,
    handleNameSave,
    handleUpload,
    handleQuery,
  } = useKnowledgeDetailPage();

  if (statsLoading && !stats) {
    return <WorkspaceDetailSkeleton />;
  }

  return (
    <div>
      <WorkspaceDetailHeader
        name={name}
        description={stats?.description}
        onBack={() => navigate('/knowledge')}
        onDescriptionSave={isAdmin ? handleDescriptionSave : undefined}
        onNameSave={isAdmin ? handleNameSave : undefined}
      />

      <WorkspaceStatsCard stats={stats ?? undefined} />

      {isAdmin && (
        <WorkspaceConfigForm form={configForm} loading={configLoading} onSubmit={handleConfigSave} />
      )}

      {isAdmin && <WorkspaceUploadZone loading={uploadLoading} onUpload={handleUpload} />}

      <WorkspaceDocumentsTable documents={documents} loading={documentsLoading} />

      <WorkspaceQueryPanel
        form={queryForm}
        loading={queryLoading}
        result={queryResult}
        onSubmit={handleQuery}
      />
    </div>
  );
};

export default KnowledgeDetailPage;
