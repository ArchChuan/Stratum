import { Form, message } from 'antd';
import { useCallback, useState } from 'react';
import { useNavigate } from 'react-router-dom';

import { skillApi } from '../api/skill.api';
import { buildCreateSkillDraftPayload, type SkillFormValues } from '../model/skill';

import { extractErrorMessage } from '@/shared/lib';

export const useCreateSkillPage = () => {
  const [form] = Form.useForm<SkillFormValues>();
  const [loading, setLoading] = useState(false);
  const navigate = useNavigate();
  const onFinish = useCallback(async (values: SkillFormValues) => {
    const payload = buildCreateSkillDraftPayload(values);
    setLoading(true);
    try {
      const workspace = await skillApi.createDraft(payload);
      message.success(`技能 "${payload.name}" 草稿已创建`);
      navigate(`/skills/${workspace.skill.id}/workspace`);
    } catch (err: unknown) {
      if ((err as { response?: { status?: number } })?.response?.status !== 403) {
        message.error(extractErrorMessage(err) || '创建失败');
      }
    } finally {
      setLoading(false);
    }
  }, [navigate]);
  return { form, loading, navigate, onFinish };
};
