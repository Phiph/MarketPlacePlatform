import { Fragment } from 'react'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { getPath, setPath } from '@/lib/object-path'
import type { JsonSchema } from '@/lib/types'

const MAX_NESTING = 3

interface SchemaFormProps {
  schema?: JsonSchema
  value: Record<string, unknown>
  onChange: (value: Record<string, unknown>) => void
}

// Renders a form from a Promise's spec JSON Schema: one input per property,
// switching on `type`. Nested objects recurse (up to MAX_NESTING); arrays
// and anything past that depth fall back to a raw-JSON textarea so unusual
// shapes still work, just without per-field affordances.
export function SchemaForm({ schema, value, onChange }: SchemaFormProps) {
  if (!schema?.properties) {
    return <p className="text-sm text-muted-foreground">This service takes no request parameters.</p>
  }

  const required = new Set(schema.required ?? [])

  return (
    <div className="space-y-4">
      {Object.entries(schema.properties).map(([key, propSchema]) => (
        <FormField
          key={key}
          name={key}
          schema={propSchema}
          required={required.has(key)}
          path={[key]}
          value={value}
          onChange={onChange}
          depth={0}
        />
      ))}
    </div>
  )
}

interface FormFieldProps {
  name: string
  schema: JsonSchema
  required: boolean
  path: string[]
  value: Record<string, unknown>
  onChange: (value: Record<string, unknown>) => void
  depth: number
}

function FormField({ name, schema, required, path, value, onChange, depth }: FormFieldProps) {
  const id = path.join('.')
  const current = getPath(value, path)
  const set = (v: unknown) => onChange(setPath(value, path, v))

  if (schema.type === 'object' && schema.properties && depth < MAX_NESTING) {
    const nestedRequired = new Set(schema.required ?? [])
    return (
      <fieldset className="space-y-4 rounded-lg border p-4">
        <legend className="px-1 text-sm font-medium">
          {name}
          {schema.description && <p className="mt-0.5 text-xs font-normal text-muted-foreground">{schema.description}</p>}
        </legend>
        {Object.entries(schema.properties).map(([childKey, childSchema]) => (
          <FormField
            key={childKey}
            name={childKey}
            schema={childSchema}
            required={nestedRequired.has(childKey)}
            path={[...path, childKey]}
            value={value}
            onChange={onChange}
            depth={depth + 1}
          />
        ))}
      </fieldset>
    )
  }

  if (schema.type === 'boolean') {
    return (
      <div className="flex items-center justify-between rounded-lg border p-3">
        <div>
          <Label htmlFor={id}>{fieldLabel(name, required)}</Label>
          {schema.description && <p className="text-xs text-muted-foreground">{schema.description}</p>}
        </div>
        <Switch id={id} checked={Boolean(current)} onCheckedChange={set} />
      </div>
    )
  }

  if (schema.enum && schema.enum.length > 0) {
    return (
      <div className="space-y-1.5">
        <Label htmlFor={id}>{fieldLabel(name, required)}</Label>
        <Select value={current != null ? String(current) : undefined} onValueChange={set}>
          <SelectTrigger id={id} className="w-full">
            <SelectValue placeholder="Select…" />
          </SelectTrigger>
          <SelectContent>
            {schema.enum.map((option) => (
              <SelectItem key={String(option)} value={String(option)}>
                {String(option)}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        {schema.description && <p className="text-xs text-muted-foreground">{schema.description}</p>}
      </div>
    )
  }

  if (schema.type === 'number' || schema.type === 'integer') {
    return (
      <div className="space-y-1.5">
        <Label htmlFor={id}>{fieldLabel(name, required)}</Label>
        <Input
          id={id}
          type="number"
          step={schema.type === 'integer' ? 1 : 'any'}
          min={schema.minimum}
          max={schema.maximum}
          value={current == null ? '' : String(current)}
          onChange={(e) => set(e.target.value === '' ? undefined : Number(e.target.value))}
        />
        {schema.description && <p className="text-xs text-muted-foreground">{schema.description}</p>}
      </div>
    )
  }

  if (schema.type === 'string') {
    return (
      <div className="space-y-1.5">
        <Label htmlFor={id}>{fieldLabel(name, required)}</Label>
        <Input
          id={id}
          value={typeof current === 'string' ? current : ''}
          placeholder={schema.default ? String(schema.default) : undefined}
          onChange={(e) => set(e.target.value)}
        />
        {schema.description && <p className="text-xs text-muted-foreground">{schema.description}</p>}
      </div>
    )
  }

  // Arrays and anything else: raw JSON, parsed on submit.
  const raw = current === undefined ? '' : typeof current === 'string' ? current : JSON.stringify(current)
  return (
    <div className="space-y-1.5">
      <Label htmlFor={id}>{fieldLabel(name, required)}</Label>
      <Textarea
        id={id}
        className="font-mono text-xs"
        rows={3}
        placeholder={schema.type === 'array' ? '["item1", "item2"]' : 'JSON value'}
        value={raw}
        onChange={(e) => set(parseLoosely(e.target.value))}
      />
      {schema.description && <p className="text-xs text-muted-foreground">{schema.description}</p>}
    </div>
  )
}

function fieldLabel(name: string, required: boolean) {
  return (
    <Fragment>
      {name}
      {required && <span className="text-destructive"> *</span>}
    </Fragment>
  )
}

function parseLoosely(raw: string): unknown {
  if (raw.trim() === '') return undefined
  try {
    return JSON.parse(raw)
  } catch {
    return raw
  }
}
