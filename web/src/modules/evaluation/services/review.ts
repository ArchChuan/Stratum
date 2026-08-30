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
