import { Bar, BarChart, CartesianGrid, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts';

interface FrecencyChartProps {
  data: number[];
}

export const FrecencyChart = ({ data }: FrecencyChartProps) => {
  const chartData = data.map((value, index) => ({
    bucket: `${(index * 0.1).toFixed(1)}-${((index + 1) * 0.1).toFixed(1)}`,
    count: value,
  }));

  return (
    <ResponsiveContainer width="100%" height={300}>
      <BarChart data={chartData}>
        <CartesianGrid strokeDasharray="3 3" />
        <XAxis dataKey="bucket" />
        <YAxis />
        <Tooltip />
        <Bar dataKey="count" fill="#1890ff" />
      </BarChart>
    </ResponsiveContainer>
  );
};
