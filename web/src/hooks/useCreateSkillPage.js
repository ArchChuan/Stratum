import { useState, useEffect, useCallback } from 'react';
import { message, Form } from 'antd';
import { useNavigate } from 'react-router-dom';
import { createSkill, getAvailableModels } from '../services/api';

const FALLBACK_MODELS = ['glm-4', 'glm-4-flash', 'qwen-plus', 'qwen-turbo'];

const useCreateSkillPage = () => {
  const [form] = Form.useForm();
  const [loading, setLoading] = useState(false);
  const [availableModels, setAvailableModels] = useState([]);
  const [modelsLoading, setModelsLoading] = useState(true);
  const navigate = useNavigate();
  const skillType = Form.useWatch('type', form);
  const language = Form.useWatch('language', form);

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
      await createSkill(values);
      message.success(`技能 "${values.name}" 创建成功`);
      navigate('/skills');
    } catch (err) {
      if (err.response?.status === 400) {
        const analysisErrors = err.response?.data?.analysis_errors;
        if (analysisErrors?.length) {
          message.error({
            content: '代码安全检测失败：' + analysisErrors.map(e => `\n• ${e}`).join(''),
            duration: 8,
          });
        } else {
          message.error(err.response?.data?.error || '创建失败');
        }
      } else if (err.response?.status !== 403) {
        message.error(err.response?.data?.error || '创建失败');
      }
    } finally {
      setLoading(false);
    }
  }, [navigate]);

  return {
    form, loading, availableModels, modelsLoading,
    skillType, language, navigate, onFinish,
  };
};

export default useCreateSkillPage;
