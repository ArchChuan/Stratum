import { useState, useEffect, useCallback } from 'react';
import { message, Form } from 'antd';
import { useNavigate, useParams } from 'react-router-dom';
import { getSkillById, updateSkill, getAvailableModels } from '../services/api';

const FALLBACK_MODELS = ['glm-4', 'glm-4-flash', 'qwen-plus', 'qwen-turbo'];

const useEditSkillPage = () => {
  const { id } = useParams();
  const [form] = Form.useForm();
  const [loading, setLoading] = useState(false);
  const [fetchLoading, setFetchLoading] = useState(true);
  const [availableModels, setAvailableModels] = useState([]);
  const [modelsLoading, setModelsLoading] = useState(true);
  const [skillType, setSkillType] = useState(null);
  const navigate = useNavigate();
  const language = Form.useWatch('language', form);

  useEffect(() => {
    let cancelled = false;
    getSkillById(id)
      .then((res) => {
        if (cancelled) return;
        const skill = res.data;
        setSkillType(skill.type);
        const cfg = skill.config || {};
        const initialValues = {
          name: skill.name,
          description: skill.description,
          type: skill.type,
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
          initialValues.headersJson = cfg.headers && Object.keys(cfg.headers).length > 0
            ? JSON.stringify(cfg.headers, null, 2)
            : '';
          initialValues.bodyTemplate = cfg.body_template || '';
        }
        form.setFieldsValue(initialValues);
      })
      .catch(() => {
        if (!cancelled) message.error('加载技能失败');
      })
      .finally(() => {
        if (!cancelled) setFetchLoading(false);
      });
    return () => { cancelled = true; };
  }, [id, form]);

  useEffect(() => {
    let cancelled = false;
    getAvailableModels()
      .then((res) => {
        if (!cancelled) {
          const models = res.data.models?.length > 0 ? res.data.models : FALLBACK_MODELS;
          setAvailableModels(models);
        }
      })
      .catch(() => {
        if (!cancelled) setAvailableModels(FALLBACK_MODELS);
      })
      .finally(() => {
        if (!cancelled) setModelsLoading(false);
      });
    return () => { cancelled = true; };
  }, []);

  const onFinish = useCallback(async (values) => {
    if (values.type === 'http' && values.headersJson) {
      try {
        values.headers = JSON.parse(values.headersJson);
      } catch {
        message.error('请求头 JSON 格式有误');
        return;
      }
      delete values.headersJson;
    }
    setLoading(true);
    try {
      await updateSkill(id, values);
      message.success(`技能 "${values.name}" 已更新`);
      navigate('/skills');
    } catch (err) {
      message.error(err.response?.data?.error || '更新失败');
    } finally {
      setLoading(false);
    }
  }, [id, navigate]);

  return {
    form, loading, fetchLoading, availableModels, modelsLoading,
    skillType, language, navigate, onFinish,
  };
};

export default useEditSkillPage;
