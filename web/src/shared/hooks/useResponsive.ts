import { useEffect, useState } from 'react';

const MOBILE_QUERY = '(max-width: 767px)';
const COMPACT_QUERY = '(max-width: 1023px)';

export const useMediaQuery = (query: string): boolean => {
  const getMatches = () => typeof window !== 'undefined'
    && typeof window.matchMedia === 'function'
    && window.matchMedia(query).matches;
  const [matches, setMatches] = useState(getMatches);

  useEffect(() => {
    if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') return undefined;

    const mediaQuery = window.matchMedia(query);
    const handleChange = (event: MediaQueryListEvent) => setMatches(event.matches);

    setMatches(mediaQuery.matches);
    mediaQuery.addEventListener('change', handleChange);
    return () => mediaQuery.removeEventListener('change', handleChange);
  }, [query]);

  return matches;
};

export const useResponsive = () => ({
  isMobile: useMediaQuery(MOBILE_QUERY),
  isCompact: useMediaQuery(COMPACT_QUERY),
});
