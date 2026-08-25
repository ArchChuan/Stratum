import { useEffect, useState } from 'react';

import { tenantApi } from '../api/tenant.api';
import type { Member } from '../model/auth';

// 可编辑人候选：租户内全部成员（含 member），白名单语义——管理员可把任意成员加入可编辑人。
// 后端 editorEligible 已放宽到所有租户成员，此处不再按 role 过滤。
export const useEditorCandidates = (): { candidates: Member[]; loading: boolean } => {
  const [candidates, setCandidates] = useState<Member[]>([]);
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    tenantApi.members(1, 1000)
      .then((page) => {
        if (!cancelled) setCandidates(page.members);
      })
      .catch(() => {
        // 候选集加载失败不阻塞表单：后端仍会在写时校验 editor 资格。
        if (!cancelled) setCandidates([]);
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, []);

  return { candidates, loading };
};
