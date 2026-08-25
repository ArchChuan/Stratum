import { useEffect, useState } from 'react';

import { tenantApi } from '../api/tenant.api';
import type { Member } from '../model/auth';

// 可编辑人候选：租户全量成员（P2 白名单语义：可编辑人从管理员扩展为任意成员）。
// 后端按页返回；当前租户规模下一页拉齐即可。
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
