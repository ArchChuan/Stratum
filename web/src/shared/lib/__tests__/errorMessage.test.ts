import { describe, it, expect } from 'vitest';

import { extractErrorMessage } from '../errorMessage';

describe('extractErrorMessage', () => {
  it('优先返回 axios 响应中的 error 字段', () => {
    const err = { response: { data: { error: '具体错误', message: '次级' } } };
    expect(extractErrorMessage(err)).toBe('具体错误');
  });

  it('error 缺失时使用 message 字段', () => {
    const err = { response: { data: { message: '后端消息' } } };
    expect(extractErrorMessage(err)).toBe('后端消息');
  });

  it('非 axios Error 返回 err.message', () => {
    expect(extractErrorMessage(new Error('网络异常'))).toBe('网络异常');
  });

  it('完全未知输入回退到 fallback', () => {
    expect(extractErrorMessage(null)).toBe('操作失败');
    expect(extractErrorMessage(undefined, '自定义')).toBe('自定义');
  });

  it('空字符串视为缺失，回退 fallback', () => {
    const err = { response: { data: { error: '', message: '' } } };
    expect(extractErrorMessage(err, '兜底')).toBe('兜底');
  });
});
