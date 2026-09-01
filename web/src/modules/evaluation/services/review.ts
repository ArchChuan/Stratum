import type { ReviewItem, ReviewItemDecisionRequest } from '../types/review';

import client from '@/services/client';

export async function listReviewItems(params: {
  status?: string;
  trigger_reason?: string;
  page?: number;
  page_size?: number;
}): Promise<{ items: ReviewItem[]; total: number }> {
  const { data } = await client.get('/evaluations/review', { params });
  return data;
}

export async function getReviewItem(id: string): Promise<ReviewItem> {
  const { data } = await client.get(`/evaluations/review/${id}`);
  return data;
}

export async function decideReviewItem(
  id: string,
  body: ReviewItemDecisionRequest,
): Promise<ReviewItem> {
  const { data } = await client.post(`/evaluations/review/${id}/decision`, body);
  return data;
}

// deleteReviewItem 删除评审项（204 空体）。RBAC：租户 owner 恒可删；系统入池项
// created_by 恒 '' 仅 owner 可删；创建者可删。
export async function deleteReviewItem(id: string): Promise<void> {
  await client.delete(`/evaluations/review/${encodeURIComponent(id)}`);
}
