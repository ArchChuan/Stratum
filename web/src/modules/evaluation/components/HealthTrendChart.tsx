// HealthTrendChart 渲染单序列健康分（通过率）随运行时间的 SVG 折线图。
// 原生 SVG 实现：单序列无需图例框，标题即标识；通过/未通过不以颜色单独编码，
// 同时使用实心圆点（通过）与红色叉号（未通过）做形态区分，满足 CVD 可读性。
// x 轴按运行序号等距排布，刻度标签最多展示 6 个避免碰撞。

export type HealthTrendPoint = {
  id: string;
  timeLabel: string;
  fullLabel: string;
  /** 健康分 [0,1]；无有效分母时为 null（不参与连线，但仍展示状态标记）。 */
  passRate: number | null;
  passed: boolean;
};

const WIDTH = 640;
const HEIGHT = 220;
const MARGIN = { top: 14, right: 14, bottom: 30, left: 42 };
const PLOT_WIDTH = WIDTH - MARGIN.left - MARGIN.right;
const PLOT_HEIGHT = HEIGHT - MARGIN.top - MARGIN.bottom;
const MAX_TICK_LABELS = 6;

const passColor = '#52c41a';
const failColor = '#ff4d4f';
const lineColor = '#1677ff';
const gridColor = '#e5e5e5';
const textColor = '#6b6b6b';

const xOf = (i: number, n: number) => (n === 1 ? MARGIN.left + PLOT_WIDTH / 2 : MARGIN.left + (i / (n - 1)) * PLOT_WIDTH);
const yOf = (value: number) => MARGIN.top + (1 - value) * PLOT_HEIGHT;

// tickSubset 在 n 个点中均匀取最多 MAX_TICK_LABELS 个下标作为刻度标签。
function tickSubset(n: number): number[] {
  if (n <= MAX_TICK_LABELS) {
    return Array.from({ length: n }, (_, i) => i);
  }
  const step = (n - 1) / (MAX_TICK_LABELS - 1);
  return Array.from({ length: MAX_TICK_LABELS }, (_, i) => Math.round(i * step));
}

export const HealthTrendChart = ({ points }: { points: HealthTrendPoint[] }) => {
  const n = points.length;
  // lineSegments 只在相邻且均有健康分的点之间连线；无健康分点（如无有效分母）断开。
  const lineSegments: string[] = [];
  let pen: string | null = null;
  for (let i = 0; i < n; i += 1) {
    const point = points[i];
    if (point.passRate === null) {
      pen = null;
      continue;
    }
    const command = `${xOf(i, n).toFixed(1)} ${yOf(point.passRate).toFixed(1)}`;
    lineSegments.push(pen === null ? `M${command}` : `${pen}L${command}`);
    pen = '';
  }
  const line = lineSegments.join(' ');
  const plottableCount = points.reduce((acc, point) => acc + (point.passRate === null ? 0 : 1), 0);

  const gridLines = [0, 0.25, 0.5, 0.75, 1];
  const ticks = tickSubset(n);

  return (
    <div data-testid="health-trend-chart">
      <svg width={WIDTH} height={HEIGHT} viewBox={`0 0 ${WIDTH} ${HEIGHT}`} role="img" aria-label="运行健康分趋势折线图"
        style={{ width: '100%', height: 'auto', display: 'block' }}>
        {gridLines.map((ratio) => (
          <g key={ratio}>
            <line x1={MARGIN.left} x2={WIDTH - MARGIN.right} y1={yOf(ratio)} y2={yOf(ratio)}
              stroke={gridColor} strokeWidth={1} />
            <text x={MARGIN.left - 6} y={yOf(ratio) + 4} textAnchor="end" fontSize={11} fill={textColor}>
              {Math.round(ratio * 100)}%
            </text>
          </g>
        ))}
        {plottableCount > 1 && <path d={line} fill="none" stroke={lineColor} strokeWidth={2} strokeLinejoin="round" />}
        {points.map((point, i) => {
          const cx = xOf(i, n);
          if (point.passRate !== null) {
            return (
              <g key={point.id}>
                {point.passed
                  ? <circle cx={cx} cy={yOf(point.passRate)} r={4} fill={passColor} stroke="#fff" strokeWidth={1} />
                  : <path d={`M${cx - 4} ${yOf(point.passRate) - 4} L${cx + 4} ${yOf(point.passRate) + 4} M${cx + 4} ${yOf(point.passRate) - 4} L${cx - 4} ${yOf(point.passRate) + 4}`}
                    stroke={failColor} strokeWidth={2} strokeLinecap="round" />}
                <title>{point.fullLabel}</title>
              </g>
            );
          }
          return <rect key={point.id} x={cx - 2} y={yOf(0.5) - 2} width={4} height={4} fill={textColor} />;
        })}
        {ticks.map((i) => {
          const point = points[i];
          if (!point) return null;
          return (
            <text key={`${point.id}-tick`} x={xOf(i, n)} y={HEIGHT - MARGIN.bottom + 16} textAnchor="middle" fontSize={10}
              fill={textColor}>
              {point.timeLabel}
            </text>
          );
        })}
      </svg>
      <div style={{ marginTop: 6, fontSize: 12, color: textColor }}>
        <span style={{ marginRight: 14 }}>
          <svg width={10} height={10} style={{ verticalAlign: -1, marginRight: 4 }}><circle cx={5} cy={5} r={4} fill={passColor} /></svg>
          通过
        </span>
        <span>
          <svg width={10} height={10} style={{ verticalAlign: 0, marginRight: 4 }}>
            <path d="M1 1 L9 9 M9 1 L1 9" stroke={failColor} strokeWidth={2} />
          </svg>
          未通过
        </span>
      </div>
    </div>
  );
};
