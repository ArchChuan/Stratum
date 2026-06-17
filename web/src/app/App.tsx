import { AppProviders } from './providers';
import { AppRouter } from './router';

export const App = () => (
  <AppProviders>
    <AppRouter />
  </AppProviders>
);

export default App;
