export const acceptanceError = (primary: unknown, secondary: unknown): unknown => {
  return acceptanceErrors([primary, secondary]);
};

export const acceptanceErrors = (values: unknown[]): unknown => {
  const failures = values.filter((value) => value !== undefined && value !== null);
  if (failures.length === 0) return undefined;
  if (failures.length === 1) return failures[0];
  return new AggregateError(failures, 'stateful acceptance and cleanup both failed');
};
