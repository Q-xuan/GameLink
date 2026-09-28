fn main() {
    println!("cargo:rerun-if-changed=app.manifest");
    let target = std::env::var("CARGO_CFG_TARGET_OS").unwrap_or_default();
    if target != "windows" {
        return;
    }
    let host = std::env::var("HOST").unwrap_or_default();
    if !host.contains("windows") {
        println!("cargo:warning=skipping administrator manifest embed on a non-Windows host");
        return;
    }
    let mut res = winresource::WindowsResource::new();
    res.set_manifest_file("app.manifest");
    res.compile().expect("embed requireAdministrator manifest");
}
