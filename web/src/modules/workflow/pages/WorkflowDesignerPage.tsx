import { Form, Modal, Result, Skeleton } from 'antd';
import { useEffect } from 'react';
import { useNavigate, useParams } from 'react-router-dom';

import { WorkflowCanvas } from '../components/WorkflowCanvas';
import { WorkflowDesignerHeader } from '../components/WorkflowDesignerHeader';
import { WorkflowInputSchemaEditor } from '../components/WorkflowInputSchemaEditor';
import { WorkflowMetadataForm } from '../components/WorkflowMetadataForm';
import { WorkflowNodeInspector } from '../components/WorkflowNodeInspector';
import { WorkflowValidationPanel } from '../components/WorkflowValidationPanel';
import { useWorkflowDesigner } from '../hooks/useWorkflowDesigner';
import { useWorkflowResources } from '../hooks/useWorkflowResources';
import type { WorkflowInputSchema } from '../model/workflow';

import { useResponsive } from '@/shared/hooks';

const WorkflowDesignerDesktop = () => {
  const { id } = useParams();
  const navigate = useNavigate();
  const designer = useWorkflowDesigner(id);
  const resources = useWorkflowResources();
  const [form] = Form.useForm();

  useEffect(() => {
    form.setFieldsValue({
      name: designer.name,
      description: designer.description,
      task_label: designer.inputSchema.task_label,
      task_description: designer.inputSchema.task_description,
      fields: designer.inputSchema.fields,
    });
  }, [designer.description, designer.inputSchema, designer.name, form]);

  if (designer.loading) return <Skeleton active />;

  const selectedNode = designer.editor.selected?.kind === 'node'
    ? designer.editor.spec.nodes.find((node) => node.id === designer.editor.selected?.id)
    : undefined;
  const validated = Boolean(designer.definition && designer.validatedRevision === designer.definition.revision);
  const save = async () => {
    try {
      await form.validateFields();
    } catch {
      return;
    }
    const saved = await designer.save();
    if (!id && saved) navigate(`/workflows/${saved.id}/edit`, { replace: true });
  };
  const publish = () => Modal.confirm({
    title: '发布当前工作流？',
    content: '发布后会生成不可修改的版本，后续变更需要保存为新的草稿修订。',
    okText: '确认发布',
    cancelText: '取消',
    onOk: async () => {
      const version = await designer.publish();
      if (version && designer.definition) navigate(`/workflows/${designer.definition.id}/versions/${version.id}`);
    },
  });

  return <section className="workflow-page-shell workflow-designer-page">
    <WorkflowDesignerHeader
      name={designer.name}
      revision={designer.definition?.revision}
      dirty={designer.dirty}
      saving={designer.saving}
      validating={designer.validating}
      publishing={designer.publishing}
      canValidate={Boolean(designer.definition) && !designer.dirty}
      canPublish={validated && !designer.dirty}
      onBack={() => navigate('/workflows')}
      onSave={save}
      onValidate={designer.validate}
      onPublish={publish}
    />
    <WorkflowValidationPanel validated={validated} />
    <div className="workflow-designer-grid">
      <div className="workflow-designer-main">
        <Form
          form={form}
          layout="vertical"
          onValuesChange={(_, values) => {
            designer.setName(values.name || '');
            designer.setDescription(values.description || '');
            designer.setInputSchema({
              task_label: values.task_label || '',
              task_description: values.task_description || '',
              fields: values.fields || [],
            } as WorkflowInputSchema);
          }}
        >
          <div className="workflow-definition-panel"><WorkflowMetadataForm /><WorkflowInputSchemaEditor /></div>
        </Form>
        <WorkflowCanvas
          state={designer.editor}
          dispatch={designer.dispatch}
          createNodeId={() => crypto.randomUUID()}
          createEdgeId={() => crypto.randomUUID()}
        />
      </div>
      {selectedNode
        ? <WorkflowNodeInspector
          node={selectedNode}
          onChange={(node) => designer.dispatch({ type: 'node.update', node })}
          {...resources}
        />
        : <aside className="workflow-node-inspector workflow-inspector-empty">选择一个节点后在这里配置。</aside>}
    </div>
  </section>;
};

export const WorkflowDesignerPage = () => {
  const { isMobile } = useResponsive();
  return isMobile
    ? <Result status="info" title="请使用桌面端编辑工作流" subTitle="手机端可以运行和查看工作流，但图形化编排需要更大的屏幕。" />
    : <WorkflowDesignerDesktop />;
};
