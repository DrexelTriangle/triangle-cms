import "trix"

declare global {
  // The attributes an attachment is constructed with. These are what Trix
  // serializes into the figure's data-trix-attachment JSON, so anything added
  // here survives a save/load round trip -- which is how alt text is carried.
  interface TrixAttachmentAttributes {
    url: string
    href?: string
    contentType?: string
    filename?: string
    filesize?: number
    width?: number
    height?: number
    alt?: string
    previewable?: boolean
  }

  interface Window {
    Trix?: {
      config: {
        attachments: {
          preview: {
            caption: {
              name: boolean
              size: boolean
            }
          }
        }
      }
      Attachment: new (attributes: TrixAttachmentAttributes) => TrixAttachment
    }
  }

  interface TrixDocument {
    getAttachments(): TrixAttachment[]
  }

  interface TrixEditorInternal {
    loadHTML(html: string): void
    getDocument(): TrixDocument
    getSelectedRange(): [number, number]
    setSelectedRange(range: [number, number] | number): void
    deleteInDirection(direction: "forward" | "backward"): void
    insertHTML(html: string): void
    insertAttachment(attachment: TrixAttachment): void
    insertLineBreak(): void
    activateAttachment(attachment: TrixAttachment): void
  }

  interface TrixEditorElement extends HTMLElement {
    editor: TrixEditorInternal
    value: string
  }

  interface TrixAttachment {
    id: number
    file: File | null
    getAttribute(name: string): unknown
    getAttributes(): Partial<TrixAttachmentAttributes>
    setUploadProgress(value: number): void
    setAttributes(attrs: Partial<TrixAttachmentAttributes>): void
    remove(): void
  }

  interface TrixAttachmentAddEvent extends Event {
    attachment: TrixAttachment
  }
}

declare module "react" {
  namespace JSX {
    interface IntrinsicElements {
      "trix-editor": {
        toolbar?: string
        className?: string
        autofocus?: boolean
        [key: string]: unknown
      }
      "trix-toolbar": {
        id?: string
        children?: import("react").ReactNode
        [key: string]: unknown
      }
    }
  }
}
