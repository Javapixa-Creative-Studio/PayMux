import { createBrowserRouter, Navigate } from 'react-router-dom';

import { AppLayout } from '../layouts/AppLayout';
import { ApplicationDetailPage } from '../pages/ApplicationDetailPage';
import { ApplicationsPage } from '../pages/ApplicationsPage';
import { DeliveriesPage } from '../pages/DeliveriesPage';
import { EventsPage } from '../pages/EventsPage';
import { GatewaysPage } from '../pages/GatewaysPage';
import { NotificationsPage } from '../pages/NotificationsPage';
import { OverviewPage } from '../pages/OverviewPage';
import { PaymentDetailPage } from '../pages/PaymentDetailPage';
import { PaymentsPage } from '../pages/PaymentsPage';
import { SettingsPage } from '../pages/SettingsPage';
import { SignInPage } from '../pages/SignInPage';
import { SubscriptionsPage } from '../pages/SubscriptionsPage';
import { RequireSession } from './RequireSession';

export const router = createBrowserRouter([
  { path: '/signin', element: <SignInPage /> },
  {
    path: '/',
    element: <RequireSession />,
    children: [
      {
        element: <AppLayout />,
        children: [
          { index: true, element: <Navigate to="/overview" replace /> },
          { path: 'overview', element: <OverviewPage /> },
          { path: 'payments', element: <PaymentsPage /> },
          { path: 'payments/:paymentId', element: <PaymentDetailPage /> },
          { path: 'events', element: <EventsPage /> },
          { path: 'deliveries', element: <DeliveriesPage /> },
          { path: 'notifications', element: <NotificationsPage /> },
          { path: 'applications', element: <ApplicationsPage /> },
          { path: 'applications/:applicationId', element: <ApplicationDetailPage /> },
          { path: 'subscriptions', element: <SubscriptionsPage /> },
          { path: 'gateways', element: <GatewaysPage /> },
          { path: 'settings', element: <SettingsPage /> },
          { path: '*', element: <Navigate to="/overview" replace /> },
        ],
      },
    ],
  },
]);
