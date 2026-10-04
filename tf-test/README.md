# tf-test

Local dummy resources for trying out tf-mankeli. Uses only the `random` and
`local` providers and the built-in `terraform_data`, so it needs no cloud
credentials. State is stored locally in `terraform.tfstate`.

## Setup

```sh
cd tf-test
terraform init
terraform apply -auto-approve
cd ..
go run ./cmd tf-test
```

Right after apply, the plan shows no changes.

## Producing changes

Edit `terraform.tfvars`, then press shift+p in the app to plan again.

| Edit                         | Resource                                   | Action |
|------------------------------|--------------------------------------------|--------|
| `pet_count = 4`              | `module.pets.random_pet.this[3]`           | `+`    |
| `pet_count = 2`              | `module.pets.random_pet.this[2]`           | `-`    |
| `greeting = "Hi"`            | `terraform_data.greeting`                  | `~`    |
|                              | `local_file.hello`                         | `-/+`  |
|                              | `data.local_file.hello`                    | `<=`   |
| `password_length = 20`       | `random_password.db`                       | `+/-`  |
|                              | `terraform_data.release` (uses the password) | `~`    |
| `release = "v2"`             | `terraform_data.release`                   | `+/-`  |
| `pet_prefix = "prod"`        | `module.pets.random_pet.this[*]`           | `-/+`  |

`random_password.db` is replaced create-first (`+/-`) rather than `-/+`
because `terraform_data.release`, which depends on it, uses
`create_before_destroy`, and Terraform applies that to its dependencies too.

Deleting `out/hello.txt` by hand also makes the plan recreate `local_file.hello`.

`random_password.db` and the `password` inside `terraform_data.release` are
sensitive and should show as `(sensitive)` in the details pane.

## Reset

```sh
terraform destroy -auto-approve
```
