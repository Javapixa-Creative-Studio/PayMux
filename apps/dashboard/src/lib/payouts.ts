import type { PayoutStatus } from '../api/types';

/**
 * What a payout's state means, in the operator's terms.
 *
 * Each line answers the only question that matters at that moment: has the
 * money left, and can anyone still stop it.
 */
export const PAYOUT_MEANING: Record<PayoutStatus, string> = {
  REQUESTED: 'Waiting for someone to release it. No money has moved.',
  APPROVED: 'Released and queued. PayMux has not sent it yet.',
  SUBMITTED: 'The gateway has it. PayMux can no longer stop it.',
  UNRESOLVED: 'Sent, but PayMux does not know the outcome. It may have gone out.',
  COMPLETED: 'The beneficiary received the money.',
  FAILED: 'The transfer did not happen. The funds stayed put.',
  REJECTED: 'Refused before anything was sent.',
};
