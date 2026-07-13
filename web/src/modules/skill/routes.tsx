import { Route } from 'react-router-dom';

import { CreateSkillPage } from './pages/CreateSkillPage';
import { EditSkillPage } from './pages/EditSkillPage';
import { SkillWorkspacePage } from './pages/SkillWorkspacePage';
import { SkillsListPage } from './pages/SkillsListPage';

import { PrivateRoute } from '@/modules/iam';

export const skillRoutes = [
  <Route
    key="skills"
    path="/skills"
    element={
      <PrivateRoute>
        <SkillsListPage />
      </PrivateRoute>
    }
  />,
  <Route
    key="skills-create"
    path="/skills/create"
    element={
      <PrivateRoute>
        <CreateSkillPage />
      </PrivateRoute>
    }
  />,
  <Route
    key="skills-workspace"
    path="/skills/:id/workspace"
    element={
      <PrivateRoute>
        <SkillWorkspacePage />
      </PrivateRoute>
    }
  />,
  <Route
    key="skills-edit"
    path="/skills/:id/edit"
    element={
      <PrivateRoute>
        <EditSkillPage />
      </PrivateRoute>
    }
  />,
];
