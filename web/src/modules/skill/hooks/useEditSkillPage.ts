import { Form, message } from 'antd';
import { useCallback, useEffect, useState } from 'react';
import { useNavigate, useParams } from 'react-router-dom';

import { skillApi } from '../api/skill.api';
import type { SkillFormValues, SkillType } from '../model/skill';

import { extractErrorMessage } from '@/shared/lib';

const FALLBACK_MODELS = ['glm-4', 'glm-4-flash', 'qwen-plus', 'qwen-turbo'];

export const useEditSkillPage = () => {
  const { id = '' } = useParams<{ id: string }>();
  const [form] = Form.useForm<SkillFormValues>();
  const [loading, setLoading] = useState(false);
  const [fetchLoading, setFetchLoading] = useState(true);
  const [availableModels, setAvailableModels] = useState<string[]>([]);
  const [modelsLoading, setModelsLoading] = useState(true);
  const [skillType, setSkillType] = useState<SkillType | null>(null);
  const navigate = useNavigate();
  const language = Form.useWatch('language', form);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const skill = await skillApi.get(id);
        if (cancelled) return;
        setSkillType(skill.type as SkillType);
        const cfg = skill.config || {};
        const initialValues: Partial<SkillFormValues> = {
          name: skill.name,
          description: skill.description,
          type: skill.type as SkillType,
        };
        if (skill.type === 'code') {
          initialValues.language = cfg.language || 'python';
          initialValues.code = cfg.code || '';
        } else if (skill.type === 'llm') {
          initialValues.systemPrompt = cfg.system_prompt || '';
          initialValues.model = cfg.model || '';
          initialValues.temperature = cfg.temperature ?? 0.7;
          initialValues.maxTokens = cfg.max_tokens || 2048;
        } else if (skill.type === 'http') {
          initialValues.url = cfg.url || '';
          initialValues.method = cfg.method || 'POST';
          initialValues.timeoutSec = cfg.timeout_sec || 30;
          initialValues.headersJson =
            cfg.headers && Object.keys(cfg.headers).length > 0
              ? JSON.stringify(cfg.headers, null, 2)
              : '';
          initialValues.bodyTemplate = cfg.body_template || '';
        }
        form.setFieldsValue(initialValues);
      } catch {
        if (!cancelled) message.error('加载技能失败');
      } finally {
        if (!cancelled) setFetchLoading(false);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [id, form]);

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
      const payload: SkillFormValues = { ...values };
      if (payload.type === 'http' && payload.headersJson) {
        try {
          payload.headers = JSON.parse(payload.headersJson);
        } catch {
          message.error('请求头 JSON 格式有误');
          return;
        }
        delete payload.headersJson;
      }
      setLoading(true);
      try {
        await skillApi.update(id, payload);
        message.success(`技能 "${payload.name}" 已更新`);
        navigate('/skills');
      } catch (err) {
        message.error(extractErrorMessage(err) || '更新失败');
      } finally {
        setLoading(false);
      }
    },
    [id, navigate],
  );

  return {
    form,
    loading,
    fetchLoading,
    availableModels,
    modelsLoading,
    skillType,
    language,
    navigate,
    onFinish,
  };
};
