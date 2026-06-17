import { SendOutlined } from '@ant-design/icons';
import { Button, Input } from 'antd';

const { TextArea } = Input;

interface Props {
  input: string;
  setInput: (v: string) => void;
  sending: boolean;
  selectedConv: string | null;
  onSend: () => void;
}

export const ChatComposer = ({ input, setInput, sending, selectedConv, onSend }: Props) => (
  <div
    style={{
      padding: '12px 24px 16px',
      background: '#fff',
      borderTop: '1px solid #f0f0f0',
      flexShrink: 0,
    }}
  >
    <div style={{ display: 'flex', gap: 8, alignItems: 'flex-end' }}>
      <TextArea
        value={input}
        onChange={(e) => setInput(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === 'Enter' && !e.shiftKey) {
            e.preventDefault();
            onSend();
          }
        }}
        placeholder={selectedConv ? '输入消息，Enter 发送，Shift+Enter 换行' : '请先选择会话'}
        autoSize={{ minRows: 1, maxRows: 5 }}
        disabled={!selectedConv || sending}
        style={{ flex: 1, resize: 'none', fontSize: 14 }}
      />
      <Button
        type="primary"
        icon={<SendOutlined />}
        onClick={onSend}
        loading={sending}
        disabled={!selectedConv || !input.trim()}
      >
        发送
      </Button>
    </div>
  </div>
);
