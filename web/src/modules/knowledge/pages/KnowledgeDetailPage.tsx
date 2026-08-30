import { DocAccessModal } from '../components/DocAccessModal';
import { DocPreviewDrawer } from '../components/DocPreviewDrawer';
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
    canEdit,
    canRequestEditor,
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
    deletingDocumentID,
    handleConfigSave,
    handleDescriptionSave,
    handleNameSave,
    handleUpload,
    handleQuery,
    handleDeleteDocument,
    userCandidates,
    userCandidatesLoading,
    roleCandidates,
    editOpen,
    setEditOpen,
    editDoc,
    accessLoading,
    accessForm,
    handleOpenAccess,
    handleSetDocAccess,
    previewDoc,
    setPreviewDoc,
    handlePreviewDocument,
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
        onDescriptionSave={canEdit ? handleDescriptionSave : undefined}
        onNameSave={canEdit ? handleNameSave : undefined}
        canRequestEditor={canRequestEditor}
      />

      <WorkspaceStatsCard stats={stats ?? undefined} docCount={documents.length || undefined} />

      {isAdmin && (
        <WorkspaceConfigForm form={configForm} loading={configLoading} onSubmit={handleConfigSave} />
      )}

      {isAdmin && (
        <WorkspaceUploadZone
          loading={uploadLoading}
          userCandidates={userCandidates}
          userCandidatesLoading={userCandidatesLoading}
          roleCandidates={roleCandidates}
          onUpload={handleUpload}
        />
      )}

      <WorkspaceDocumentsTable
        documents={documents}
        loading={documentsLoading}
        isAdmin={isAdmin}
        deletingDocumentID={deletingDocumentID}
        onDelete={handleDeleteDocument}
        onPreview={handlePreviewDocument}
        onSetAccess={isAdmin ? handleOpenAccess : undefined}
        workspaceName={name}
      />

      <WorkspaceQueryPanel
        form={queryForm}
        loading={queryLoading}
        result={queryResult}
        onSubmit={handleQuery}
      />

      {isAdmin && (
        <DocAccessModal
          open={editOpen}
          loading={accessLoading}
          form={accessForm}
          documentTitle={editDoc?.source || editDoc?.id || ''}
          userCandidates={userCandidates}
          userCandidatesLoading={userCandidatesLoading}
          roleCandidates={roleCandidates}
          onClose={() => setEditOpen(false)}
          onSubmit={handleSetDocAccess}
        />
      )}

      <DocPreviewDrawer
        open={Boolean(previewDoc)}
        name={name}
        documentID={previewDoc?.id ?? ''}
        documentTitle={previewDoc?.source}
        onClose={() => setPreviewDoc(null)}
      />
    </div>
  );
};

export default KnowledgeDetailPage;
