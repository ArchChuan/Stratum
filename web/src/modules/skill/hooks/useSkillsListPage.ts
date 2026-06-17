import { message } from 'antd';
import { useCallback, useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';

import { skillApi } from '../api/skill.api';
import type { Skill } from '../model/skill';

import { extractErrorMessage } from '@/shared/lib';

export const useSkillsListPage = () => {
  const navigate = useNavigate();
  const [skills, setSkills] = useState<Skill[]>([]);
  const [loading, setLoading] = useState(true);
  const [searchText, setSearchText] = useState('');

  useEffect(() => {
    let cancelled = false;
    (async () => {
      setLoading(true);
      try {
        const list = await skillApi.list();
        if (!cancelled) setSkills(list);
      } catch (err) {
        if (!cancelled) message.error(extractErrorMessage(err) || '获取技能列表失败');
      } finally {
        if (!cancelled) setLoading(false);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  const handleDelete = useCallback(async (id: string) => {
    try {
      await skillApi.delete(id);
      message.success('技能已删除');
      setSkills((prev) => prev.filter((s) => s.id !== id));
    } catch (err) {
      message.error(extractErrorMessage(err) || '删除失败');
    }
  }, []);

  const filtered = skills.filter(
    (s) =>
      s.name.toLowerCase().includes(searchText.toLowerCase()) ||
      (s.description || '').toLowerCase().includes(searchText.toLowerCase()),
  );

  return { skills: filtered, loading, searchText, setSearchText, navigate, handleDelete };
};
