import { WorkspaceCreateModal } from '../components/WorkspaceCreateModal';
import { WorkspaceListGrid } from '../components/WorkspaceListGrid';
import { WorkspaceListHeader } from '../components/WorkspaceListHeader';
import { WorkspaceListSkeleton } from '../components/WorkspaceListSkeleton';
import { useKnowledgePage } from '../hooks/useKnowledgePage';

import { useEditorCandidates } from '@/modules/iam';

export const KnowledgePage = () => {
  const { candidates: editorCandidates, loading: editorCandidatesLoading } = useEditorCandidates();
  const {
    workspaces,
    loading,
    isAdmin,
    createOpen,
    setCreateOpen,
    createLoading,
    searchText,
    setSearchText,
    form,
    navigate,
    handleCreate,
    handleDelete,
  } = useKnowledgePage();

  return (
    <div>
      <WorkspaceListHeader
        searchText={searchText}
        isAdmin={isAdmin}
        onSearchChange={setSearchText}
        onCreate={() => setCreateOpen(true)}
      />

      {loading ? (
        <WorkspaceListSkeleton />
      ) : (
        <WorkspaceListGrid
          workspaces={workspaces}
          isAdmin={isAdmin}
          searchText={searchText}
          onOpen={(n) => navigate(`/knowledge/${n}`)}
          onDelete={handleDelete}
          onCreate={() => setCreateOpen(true)}
        />
      )}

      <WorkspaceCreateModal
        open={createOpen}
        loading={createLoading}
        form={form}
        editorCandidates={editorCandidates}
        editorCandidatesLoading={editorCandidatesLoading}
        onClose={() => setCreateOpen(false)}
        onSubmit={handleCreate}
      />
    </div>
  );
};

export default KnowledgePage;
