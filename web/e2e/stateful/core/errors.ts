export const acceptanceError = (primary: unknown, secondary: unknown): unknown => {
  return acceptanceErrors([primary, secondary]);
};

export const acceptanceErrors = (values: unknown[]): unknown => {
  const failures = values.filter((value) => value !== undefined && value !== null);
  if (failures.length === 0) return undefined;
  if (failures.length === 1) return failures[0];
  return new AggregateError(failures, 'stateful acceptance and cleanup both failed');
};

export const runCleanupTasks = async (tasks: Array<() => Promise<unknown>>): Promise<void> => {
  const failures: unknown[] = [];
  for (const task of tasks) {
    try {
      await task();
    } catch (error) {
      failures.push(error);
    }
  }
  const failure = acceptanceErrors(failures);
  if (failure) throw failure;
};
