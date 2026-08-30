import { HistoryOutlined } from '@ant-design/icons';
import { Button, message, Modal } from 'antd';
import { useCallback, useState } from 'react';

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
import type { KnowledgeDocument } from '../model/knowledge';

import { operationProposalApi } from '@/modules/operation-gate';
import { extractErrorMessage } from '@/shared/lib';
import { VersionHistory } from '@/shared/ui';

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
    versions,
    versionsOpen,
    setVersionsOpen,
    versionsLoading,
    openVersions,
    rollbackVersion,
    undoEdits,
  } = useKnowledgeDetailPage();

  // 成员自助「申请查看权限」：受限文档发起 grant_editor（knowledge_doc）提案，
  // 管理员在审批中心「权限审批」批准后加入该文档查看白名单，列表随即解锁。
  const [requestingDocumentID, setRequestingDocumentID] = useState<string | null>(null);
  const handleRequestAccess = useCallback(async (doc: KnowledgeDocument) => {
    setRequestingDocumentID(doc.id);
    try {
      await operationProposalApi.requestEditorAccess('knowledge_doc', doc.id, {
        workspaceName: name,
        resourceName: `${name}/${doc.source}`,
      });
      message.success({ content: '已提交，等待管理员审批', duration: 3 });
    } catch (err) {
      message.error({ content: extractErrorMessage(err, '申请查看权限失败'), duration: 3 });
    } finally {
      setRequestingDocumentID(null);
    }
  }, [name]);

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

      <WorkspaceStatsCard stats={stats ?? undefined} docCount={documents.length || undefined} />

      {isAdmin && (
        <div style={{ display: 'flex', justifyContent: 'flex-end', marginBottom: 16 }}>
          <Button icon={<HistoryOutlined />} onClick={openVersions}>
            版本历史
          </Button>
        </div>
      )}

      {isAdmin && (
        <WorkspaceConfigForm
          form={configForm}
          loading={configLoading}
          onSubmit={handleConfigSave}
          onUndo={undoEdits}
        />
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
        onRequestAccess={isAdmin ? undefined : handleRequestAccess}
        requestingDocumentID={requestingDocumentID ?? ''}
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

      {isAdmin && (
        <Modal
          title="版本历史"
          open={versionsOpen}
          onCancel={() => setVersionsOpen(false)}
          footer={null}
          width={760}
        >
          <VersionHistory
            rows={versions.map((v) => ({
              id: v.id,
              versionNo: v.versionNo,
              status: v.status,
              isCurrent: v.isCurrent,
              createdByName: v.createdByName,
              createdBy: v.createdBy,
              createdAt: v.createdAt,
              canRollback: v.status === 'deprecated' && isAdmin,
              summary: v.safeSummary,
            }))}
            loading={versionsLoading}
            rollback={rollbackVersion}
          />
        </Modal>
      )}
    </div>
  );
};

export default KnowledgeDetailPage;
