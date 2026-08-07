import { useEffect, useState } from 'react';

import { tenantApi } from '../api/tenant.api';
import type { Member } from '../model/auth';

// 可编辑人候选：租户内 admin/owner 成员全量列表。
// 后端 role 过滤路径忽略分页、全量返回，一页拉齐即可。
export const useEditorCandidates = (): { candidates: Member[]; loading: boolean } => {
  const [candidates, setCandidates] = useState<Member[]>([]);
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    tenantApi.members(1, 1000, 'admin,owner')
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
