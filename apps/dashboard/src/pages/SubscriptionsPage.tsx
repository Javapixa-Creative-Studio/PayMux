import { useSubscriptionAction, useSubscriptions } from '../api/queries';
import type { Subscription } from '../api/types';
import { Amount, Empty, ErrorNotice, Id, Loading, Tag, Timestamp } from '../components/primitives';

export function SubscriptionsPage() {
  const subscriptions = useSubscriptions({ limit: 50 });

  return (
    <>
      <div className="page__head">
        <h1>Subscriptions</h1>
      </div>
      <p className="page__lede">
        Recurring charges PayMux manages on behalf of an application. Recurring billing has to be
        activated on the gateway account before these can be created.
      </p>

      {subscriptions.isError && (
        <ErrorNotice error={subscriptions.error} action="Could not load subscriptions." />
      )}

      <div className="panel">
        <div className="panel__scroll">
          {subscriptions.isPending ? (
            <Loading rows={4} />
          ) : subscriptions.data && subscriptions.data.data.length > 0 ? (
            <table>
              <thead>
                <tr>
                  <th>Subscription</th>
                  <th>Name</th>
                  <th className="num">Amount</th>
                  <th>Interval</th>
                  <th>Status</th>
                  <th>Created</th>
                  <th />
                </tr>
              </thead>
              <tbody>
                {subscriptions.data.data.map((subscription) => (
                  <SubscriptionRow key={subscription.id} subscription={subscription} />
                ))}
              </tbody>
            </table>
          ) : (
            <Empty
              title="No subscriptions"
              hint="An application creates one by calling POST /api/v1/subscriptions with a gateway payment token."
            />
          )}
        </div>
      </div>
    </>
  );
}

function SubscriptionRow({ subscription }: { subscription: Subscription }) {
  const action = useSubscriptionAction(subscription.id);
  const active = subscription.status === 'ACTIVE';
  const canceled = subscription.status === 'CANCELED';

  return (
    <tr>
      <td data-label="Subscription" data-primary>
        <Id value={subscription.id} />
      </td>
      <td data-label="Name">{subscription.name}</td>
      <td data-label="Amount" className="num">
        <Amount minor={subscription.amount} currency={subscription.currency} />
      </td>
      <td data-label="Interval" className="gateway-status">
        every {subscription.interval_count} {subscription.interval_unit}
        {subscription.interval_count === 1 ? '' : 's'}
      </td>
      <td data-label="Status">
        <Tag tone={active ? 'settled' : canceled ? 'failed' : 'inert'}>{subscription.status}</Tag>
      </td>
      <td data-label="Created">
        <Timestamp value={subscription.created_at} />
      </td>
      <td>
        <div className="actions">
          <button
            type="button"
            className="button button--small"
            onClick={() => action.mutate('sync')}
            disabled={action.isPending}
          >
            Sync
          </button>
          {!canceled && (
            <>
              <button
                type="button"
                className="button button--small"
                onClick={() => action.mutate(active ? 'disable' : 'enable')}
                disabled={action.isPending}
              >
                {active ? 'Pause' : 'Resume'}
              </button>
              <button
                type="button"
                className="button button--small button--danger"
                onClick={() => action.mutate('cancel')}
                disabled={action.isPending}
              >
                Cancel
              </button>
            </>
          )}
        </div>
      </td>
    </tr>
  );
}
