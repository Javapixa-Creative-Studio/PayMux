import { useState } from 'react';
import { Link } from 'react-router-dom';

import { useDeliveries, useDeliveryDetail, useRetryDelivery } from '../api/queries';
import type { DeliveryState } from '../api/types';
import { Modal } from '../components/Modal';
import {
  DeliveryResult,
  DeliveryStateTag,
  Empty,
  ErrorNotice,
  Id,
  KeyValue,
  Loading,
  Timestamp,
} from '../components/primitives';

const STATES: DeliveryState[] = ['pending', 'delivering', 'succeeded', 'failed', 'dead', 'canceled'];

export function DeliveriesPage() {
  const [state, setState] = useState<string>('');
  const [inspecting, setInspecting] = useState<string | null>(null);
  const deliveries = useDeliveries({ state: state || undefined, limit: 50 });
  const retry = useRetryDelivery();

  return (
    <>
      <div className="page__head">
        <h1>Deliveries</h1>
      </div>
      <p className="page__lede">
        Every attempt PayMux has made to hand an event to an application. A delivery that gave up
        stays here so it can be replayed once the destination is healthy.
      </p>

      <div className="filters">
        <select aria-label="State" value={state} onChange={(event) => setState(event.target.value)}>
          <option value="">Any state</option>
          {STATES.map((value) => (
            <option key={value} value={value}>
              {value}
            </option>
          ))}
        </select>
        {state && (
          <button type="button" className="button button--small" onClick={() => setState('')}>
            Clear filter
          </button>
        )}
      </div>

      {(deliveries.isError || retry.isError) && (
        <ErrorNotice
          error={deliveries.error ?? retry.error}
          action={retry.isError ? 'The delivery was not requeued.' : 'Could not load deliveries.'}
        />
      )}

      <div className="panel">
        <div className="panel__scroll">
          {deliveries.isPending ? (
            <Loading rows={6} />
          ) : deliveries.data && deliveries.data.data.length > 0 ? (
            <table>
              <thead>
                <tr>
                  <th>Delivery</th>
                  <th>Destination</th>
                  <th>State</th>
                  <th className="num">Attempts</th>
                  <th>Last result</th>
                  <th className="num">Time</th>
                  <th>Next attempt</th>
                  <th />
                </tr>
              </thead>
              <tbody>
                {deliveries.data.data.map((delivery) => (
                  <tr
                    key={delivery.id}
                    className={
                      delivery.state === 'dead'
                        ? 'is-failing'
                        : delivery.state === 'failed'
                          ? 'is-attention'
                          : undefined
                    }
                  >
                    <td data-label="Delivery" data-primary="">
                      <button
                        type="button"
                        className="id"
                        onClick={() => setInspecting(delivery.id)}
                        title="Show attempt history"
                      >
                        {delivery.id}
                      </button>
                    </td>
                    <td data-label="Destination" className="mono cell--url">
                      {delivery.url}
                    </td>
                    <td data-label="State">
                      <DeliveryStateTag state={delivery.state} />
                    </td>
                    <td data-label="Attempts" className="num">
                      {delivery.attempt_count}/{delivery.max_attempts}
                    </td>
                    <td data-label="Last result">
                      <DeliveryResult
                        statusCode={delivery.last_status_code}
                        error={delivery.last_error}
                      />
                    </td>
                    <td data-label="Time" className="num gateway-status">
                      {delivery.last_duration_ms != null ? `${delivery.last_duration_ms}ms` : '—'}
                    </td>
                    <td data-label="Next attempt">
                      {delivery.state === 'succeeded' ? (
                        <span className="gateway-status">delivered</span>
                      ) : delivery.state === 'dead' ? (
                        <span className="gateway-status">gave up</span>
                      ) : (
                        <Timestamp value={delivery.next_attempt_at} />
                      )}
                    </td>
                    <td>
                      {delivery.state !== 'succeeded' && delivery.state !== 'delivering' && (
                        <button
                          type="button"
                          className="button button--small"
                          onClick={() => retry.mutate(delivery.id)}
                          disabled={retry.isPending}
                        >
                          Retry now
                        </button>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          ) : state ? (
            <Empty title={`No ${state} deliveries`} hint="Try another state, or clear the filter." />
          ) : (
            <Empty
              title="No deliveries yet"
              hint="Deliveries appear once an application has a webhook destination and an event to receive."
            />
          )}
        </div>
      </div>

      {inspecting && <AttemptsDialog id={inspecting} onClose={() => setInspecting(null)} />}
    </>
  );
}

function AttemptsDialog({ id, onClose }: { id: string; onClose: () => void }) {
  const detail = useDeliveryDetail(id);

  return (
    <Modal title="Delivery attempts" onClose={onClose}>
      {detail.isPending ? (
        <Loading rows={3} />
      ) : detail.isError || !detail.data ? (
        <ErrorNotice error={detail.error} action="Could not load this delivery." />
      ) : (
        <>
          <dl className="kv" style={{ marginBottom: 16 }}>
            <KeyValue label="Delivery">
              <Id value={detail.data.delivery.id} full />
            </KeyValue>
            <KeyValue label="Event">
              <Link to={`/events?event=${detail.data.delivery.event_id}`}>
                <Id value={detail.data.delivery.event_id} />
              </Link>
            </KeyValue>
            <KeyValue label="Destination">
              <span className="mono">{detail.data.delivery.url}</span>
            </KeyValue>
            <KeyValue label="State">
              <DeliveryStateTag state={detail.data.delivery.state} />
            </KeyValue>
          </dl>

          {detail.data.attempts && detail.data.attempts.length > 0 ? (
            <table>
              <thead>
                <tr>
                  <th className="num">#</th>
                  <th>Result</th>
                  <th className="num">Time</th>
                  <th>When</th>
                </tr>
              </thead>
              <tbody>
                {detail.data.attempts.map((attempt) => (
                  <tr key={attempt.id}>
                    <td data-label="#" className="num">{attempt.attempt_number}</td>
                    <td data-label="Result">
                      <DeliveryResult statusCode={attempt.status_code} error={attempt.error} />
                    </td>
                    <td data-label="Time" className="num gateway-status">
                      {attempt.duration_ms}ms
                    </td>
                    <td data-label="When">
                      <Timestamp value={attempt.created_at} />
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          ) : (
            <Empty title="Not attempted yet" hint="This delivery is still waiting in the queue." />
          )}
        </>
      )}
    </Modal>
  );
}
