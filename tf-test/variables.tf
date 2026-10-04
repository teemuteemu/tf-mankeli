variable "greeting" {
  type    = string
  default = "Hello, tf-mankeli!"
}

variable "release" {
  type    = string
  default = "v1"
}

variable "password_length" {
  type    = number
  default = 16
}

variable "pet_count" {
  type    = number
  default = 3
}

variable "pet_prefix" {
  type    = string
  default = "dev"
}
