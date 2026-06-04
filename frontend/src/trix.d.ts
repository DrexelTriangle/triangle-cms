import "trix"

declare global {
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
    activateAttachment(attachment: TrixAttachment): void
  }

  interface TrixEditorElement extends HTMLElement {
    editor: TrixEditorInternal
    value: string
  }

  interface TrixAttachment {
    id: number
    file: File | null
    setUploadProgress(value: number): void
    setAttributes(attrs: Partial<{ url: string; href: string }>): void
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
