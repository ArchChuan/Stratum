import { Form, message } from 'antd';
import { useCallback, useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';

import { skillApi } from '../api/skill.api';
import {
  buildCreateSkillDraftPayload,
  type SkillFormValues,
} from '../model/skill';

import { extractErrorMessage } from '@/shared/lib';

const FALLBACK_MODELS = ['glm-4', 'glm-4-flash', 'qwen-plus', 'qwen-turbo'];

export const useCreateSkillPage = () => {
  const [form] = Form.useForm<SkillFormValues>();
  const [loading, setLoading] = useState(false);
  const [availableModels, setAvailableModels] = useState<string[]>([]);
  const [modelsLoading, setModelsLoading] = useState(true);
  const navigate = useNavigate();
  const skillType = Form.useWatch('type', form);
  const language = Form.useWatch('language', form);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const models = await skillApi.listModels();
        if (!cancelled) setAvailableModels(models.length > 0 ? models : FALLBACK_MODELS);
      } catch {
        if (!cancelled) setAvailableModels(FALLBACK_MODELS);
      } finally {
        if (!cancelled) setModelsLoading(false);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  const onFinish = useCallback(
    async (values: SkillFormValues) => {
      const payload = buildCreateSkillDraftPayload(values);
      setLoading(true);
      try {
        const workspace = await skillApi.createDraft(payload);
        message.success(`技能 "${payload.name}" 草稿已创建`);
        navigate(`/skills/${workspace.skill.id}/workspace`);
      } catch (err: unknown) {
        const status = (err as { response?: { status?: number } })?.response?.status;
        if (status === 400) {
          const analysisErrors = (err as { response?: { data?: { analysis_errors?: string[] } } })
            ?.response?.data?.analysis_errors;
          if (analysisErrors?.length) {
            message.error({
              content: '代码安全检测失败：' + analysisErrors.map((e) => `\n• ${e}`).join(''),
              duration: 8,
            });
          } else {
            message.error(extractErrorMessage(err) || '创建失败');
          }
        } else if (status !== 403) {
          message.error(extractErrorMessage(err) || '创建失败');
        }
      } finally {
        setLoading(false);
      }
    },
    [navigate],
  );

  return {
    form,
    loading,
    availableModels,
    modelsLoading,
    skillType,
    language,
    navigate,
    onFinish,
  };
};
